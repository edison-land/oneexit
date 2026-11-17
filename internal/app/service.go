package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"temproom/internal/browser"
	"temproom/internal/config"
	"temproom/internal/device"
	"temproom/internal/egress"
	"temproom/internal/network"
	"temproom/internal/room"
	"temproom/internal/signaling"
	"temproom/internal/tunnel"
)

// DefaultServerURL is the built-in public relay. The open-source build has
// none: set it at build time (scripts/build.sh reads TEMPROOM_DEFAULT_RELAY)
// or at run time with TEMPROOM_SERVER_URL. Without it, rooms need a private
// Worker and invites always name their relay.
var DefaultServerURL = ""

// Data-plane states shown to the user. Only VERIFIED means B's browser really
// exits through A's proxy.
const (
	NetIdle       = "IDLE"
	NetSharing    = "SHARING"    // A: relay link up, serving partner
	NetConnecting = "CONNECTING" // B: tunnel port open, verifying exit
	NetVerified   = "VERIFIED"   // B: exit measured through the tunnel
	NetFailed     = "FAILED"     // B: traffic blocked (fail-closed)
)

type NetworkView struct {
	State          string    `json:"state"`
	Message        string    `json:"message,omitempty"`
	Warning        string    `json:"warning,omitempty"`
	ProxyAddress   string    `json:"proxyAddress,omitempty"`
	ExpectedExitIP string    `json:"expectedExitIp,omitempty"`
	ExitIP         string    `json:"exitIp,omitempty"`
	DirectIP       string    `json:"directIp,omitempty"`
	RelayOnline    bool      `json:"relayOnline"`
	Streams        int       `json:"streams"`
	CheckedAt      time.Time `json:"checkedAt,omitempty"`
}

type SessionView struct {
	room.Session
	Invite        string      `json:"invite,omitempty"`
	Relay         string      `json:"relay,omitempty"` // private relay host; "" = default relay
	Network       NetworkView `json:"network"`
	ChromeRunning bool        `json:"chromeRunning"`
}

type StepResult struct {
	Step    string `json:"step"`
	Success bool   `json:"success"`
	Skipped bool   `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

type EndInfo struct {
	Reason  string       `json:"reason"`
	Results []StepResult `json:"results"`
	At      time.Time    `json:"at"`
}

type SystemStatus struct {
	PublicIP      string            `json:"publicIp"`
	Camera        device.CameraInfo `json:"camera"`
	ChromeRunning bool              `json:"chromeRunning"`
	ServerURL     string            `json:"serverUrl,omitempty"`
}

// RelayView is the saved relay choice for new rooms.
type RelayView struct {
	Mode            string `json:"mode"` // "public" | "custom"
	CustomWorkerURL string `json:"customWorkerUrl"`
	PublicAvailable bool   `json:"publicAvailable"` // this build has a default relay
}

type RelayHealth struct {
	OK        bool   `json:"ok"`
	URL       string `json:"url"`
	Worker    bool   `json:"worker"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

type pendingLeave struct{ url, token string }

type Service struct {
	opMu sync.Mutex // serializes user operations (create/join/approve/leave)
	mu   sync.Mutex // guards the fields below; not held across network calls in sync

	gen          uint64 // bumps whenever the local session is replaced or ended
	token        string
	secret       tunnel.Secret
	invite       string
	createID     string
	joinKey      string
	joinID       string
	provider     *tunnel.Provider
	consumer     *tunnel.Consumer
	net          NetworkView
	checking     bool
	lastCheck    time.Time
	lastOK       time.Time
	lastEnd      *EndInfo
	pendingClose map[string]pendingLeave // room key -> where/how to close it
	relayURL     string                  // relay of the current session
	relayCfg     config.Config
	configDir    string
	joinRelay    string

	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once

	statusMu sync.Mutex
	pubIP    string
	pubIPAt  time.Time
	camera   device.CameraInfo
	cameraAt time.Time

	RoomMgr     *room.Manager
	Chrome      *browser.ChromeSession
	NetResolver *network.Resolver
	Probe       *egress.SOCKS5Probe // probes A's local proxy
	TunnelProbe *egress.SOCKS5Probe // measures B's exit through the tunnel
	Signaling   *signaling.Client
	// DirectIP returns this machine's own public IP (overridable in tests).
	DirectIP func(context.Context) (string, error)
	// PublicURL is the default relay (TEMPROOM_SERVER_URL overrides it).
	PublicURL string
}

func NewService() *Service {
	serverURL := os.Getenv("TEMPROOM_SERVER_URL")
	if serverURL == "" {
		serverURL = DefaultServerURL
	}
	s := &Service{
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		pendingClose: map[string]pendingLeave{},
		PublicURL:    strings.TrimRight(serverURL, "/"),

		RoomMgr:     room.NewManager(),
		Chrome:      browser.NewChromeSession(),
		NetResolver: network.NewResolver(),
		Probe:       egress.NewSOCKS5Probe(4 * time.Second),
		TunnelProbe: egress.NewSOCKS5Probe(12 * time.Second),
	}
	s.DirectIP = s.cachedPublicIP
	s.configDir, _ = config.Dir()
	s.relayCfg = config.Load(s.configDir)
	s.Signaling = signaling.NewClient(serverURL, func(string) {
		select { // events never mutate state; they only wake the poller
		case s.wake <- struct{}{}:
		default:
		}
	})
	go s.syncLoop()
	return s
}

func randID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) syncLoop() {
	t := time.NewTicker(1200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		case <-s.wake:
		}
		s.syncOnce()
	}
}

// Close stops background work. It does not notify the server; use LeaveRoom.
func (s *Service) Close() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.Signaling.DisconnectWS()
	})
}

// ServiceShutdown is called by Wails when the app quits: end the session so the
// partner loses access now instead of after the lease expires.
func (s *Service) ServiceShutdown() error {
	done := make(chan struct{})
	go func() { _, _ = s.LeaveRoom(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	s.Close()
	return nil
}

func shortErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i > 0 && len(msg) > 120 {
		msg = msg[i+2:]
	}
	if len(msg) > 160 {
		msg = msg[:160]
	}
	return msg
}

// ---- user operations ----

func (s *Service) CreateRoom(proxyAddr string) (*SessionView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if cur := s.RoomMgr.CurrentSession(); cur != nil {
		if cur.Role == room.RoleRequester {
			return s.GetSession(), nil
		}
		return nil, errors.New("leave the current session first")
	}
	s.mu.Lock()
	target := s.PublicURL
	if s.relayCfg.RelayMode == config.RelayCustom && s.relayCfg.CustomWorkerURL != "" {
		target = s.relayCfg.CustomWorkerURL
	}
	s.mu.Unlock()
	if target == "" {
		return nil, errors.New("this build has no default relay: choose My Cloudflare Worker under Relay")
	}
	s.Signaling.SetURL(target)
	proxyAddr = strings.TrimSpace(proxyAddr)
	if proxyAddr == "" {
		proxyAddr = "127.0.0.1:7890"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	probe := s.Probe.Probe(ctx, proxyAddr)
	if !probe.Available {
		return nil, fmt.Errorf("local proxy %s is not a working SOCKS5/mixed port: %s", proxyAddr, probe.Error)
	}
	if probe.ExitIP == "" {
		return nil, fmt.Errorf("proxy %s accepts connections but cannot reach the internet", proxyAddr)
	}
	direct, _ := s.DirectIP(ctx)
	warning := ""
	if direct != "" && direct == probe.ExitIP {
		warning = "Proxy exit matches this Mac's public IP. Fine if your proxy runs in TUN/global mode; otherwise check it is not in direct mode."
	}
	secret, err := tunnel.NewSecret()
	if err != nil {
		return nil, err
	}
	if s.createID == "" {
		s.createID = randID()
	}
	var rem *signaling.RemoteRoom
	for attempt := 0; attempt < 3; attempt++ {
		rem, err = s.Signaling.CreateRoom(ctx, probe.ExitIP, s.createID)
		var he *signaling.HTTPError
		if err == nil || errors.As(err, &he) || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	if err != nil {
		if target != s.PublicURL {
			return nil, fmt.Errorf("your Cloudflare Worker (%s) did not respond: %w", target, err)
		}
		return nil, fmt.Errorf("create room: %w", err)
	}
	if rem.RoomKey == "" || rem.State != "WAITING" || rem.ExpiresAt.IsZero() {
		return nil, errors.New("invalid server room response")
	}
	if rem.Token == "" {
		return nil, errors.New("signaling server is outdated (no room token); redeploy temproom-server")
	}
	s.createID = ""

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.RoomMgr.CreateRoomWithKey(rem.RoomKey, room.RoleRequester)
	s.RoomMgr.SetRemoteMetadata(rem.CreatedAt, rem.ExpiresAt, probe.ExitIP)
	s.RoomMgr.SetProxyAddress(proxyAddr)
	inviteRelay := ""
	if target != s.PublicURL {
		inviteRelay = target
	}
	s.token, s.secret, s.invite = rem.Token, secret, tunnel.FormatInvite(rem.RoomKey, secret, inviteRelay)
	s.relayURL = target
	s.net = NetworkView{State: NetIdle, ProxyAddress: proxyAddr, ExpectedExitIP: probe.ExitIP, DirectIP: direct, Warning: warning}
	s.lastEnd, s.lastOK = nil, time.Now()
	s.Signaling.Subscribe(rem.RoomKey, rem.Token)
	return s.viewLocked(), nil
}

func (s *Service) JoinRoom(invite string) (*SessionView, error) {
	key, secret, relay, err := tunnel.ParseInvite(invite)
	if err != nil {
		return nil, err
	}
	target := s.PublicURL
	if relay != "" {
		if target, err = config.NormalizeRelayURL(relay); err != nil {
			return nil, fmt.Errorf("the relay address in this invite is invalid: %w", err)
		}
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if cur := s.RoomMgr.CurrentSession(); cur != nil {
		if cur.Role == room.RoleOperator && cur.RoomKey == key {
			return s.GetSession(), nil
		}
		return nil, errors.New("leave the current session first")
	}
	if target == "" {
		return nil, errors.New("this invite has no relay address and this build has no default relay; ask the host for the full invite")
	}
	s.Signaling.SetURL(target)
	if s.joinKey != key || s.joinRelay != target {
		s.joinKey, s.joinRelay, s.joinID = key, target, randID()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rem *signaling.RemoteRoom
	for attempt := 0; attempt < 3; attempt++ {
		rem, err = s.Signaling.JoinRoom(ctx, key, s.joinID)
		var he *signaling.HTTPError
		if err == nil || errors.As(err, &he) || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	switch {
	case signaling.IsGone(err):
		return nil, errors.New("room not found or expired")
	case signaling.IsConflict(err):
		return nil, errors.New("this room already has another participant")
	case err != nil && relay != "":
		return nil, fmt.Errorf("cannot reach the host's relay %s: %w", target, err)
	case err != nil:
		return nil, fmt.Errorf("join room: %w", err)
	}
	if rem.RoomKey != key || (rem.State != "PENDING_APPROVAL" && rem.State != "ACTIVE") || rem.ExpiresAt.IsZero() {
		return nil, errors.New("invalid server room response")
	}
	if rem.Token == "" {
		return nil, errors.New("signaling server is outdated (no room token); redeploy temproom-server")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	if _, err := s.RoomMgr.JoinRoom(key, room.RoleOperator); err != nil {
		return nil, err
	}
	s.RoomMgr.SetRemoteMetadata(rem.CreatedAt, rem.ExpiresAt, rem.ExitIP)
	s.token, s.secret, s.invite = rem.Token, secret, ""
	s.relayURL = target
	s.net = NetworkView{State: NetIdle, ExpectedExitIP: rem.ExitIP}
	s.lastEnd, s.lastOK = nil, time.Now()
	s.Signaling.Subscribe(key, rem.Token)
	if rem.State == "ACTIVE" { // retry after A already approved
		_, _ = s.RoomMgr.TransitionTo(room.StateActive)
		s.ensureDataPlaneLocked()
	}
	return s.viewLocked(), nil
}

func (s *Service) ApproveJoin() (*SessionView, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	sess := s.RoomMgr.CurrentSession()
	gen, token := s.gen, s.token
	s.mu.Unlock()
	if sess == nil {
		return nil, room.ErrRoomNotFound
	}
	if sess.Role != room.RoleRequester {
		return nil, room.ErrUnauthorized
	}
	if sess.State != room.StatePendingApproval && sess.State != room.StateActive {
		return nil, errors.New("no join request to approve")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := s.Signaling.ApproveRoom(ctx, sess.RoomKey, token); err != nil {
		if signaling.IsGone(err) {
			return nil, errors.New("the room is no longer available")
		}
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return nil, errors.New("session changed during approval")
	}
	if _, err := s.RoomMgr.TransitionTo(room.StateActive); err != nil {
		return nil, err
	}
	s.ensureDataPlaneLocked()
	return s.viewLocked(), nil
}

// LeaveRoom revokes access locally first (cuts relay streams, closes the tunnel
// port and Chrome), then tells the server.
func (s *Service) LeaveRoom() ([]StepResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	sess := s.RoomMgr.CurrentSession()
	if sess == nil {
		s.mu.Unlock()
		return []StepResult{}, nil
	}
	key, token, relayURL := sess.RoomKey, s.token, s.relayURL
	results := s.endLocked("You ended the session.")
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.Signaling.LeaveRoomAt(ctx, relayURL, key, token)
	step := StepResult{Step: "Close the room on the server", Success: true}
	if err != nil && !signaling.IsGone(err) {
		step = StepResult{Step: "Close the room on the server", Error: "will retry; the server also drops it within 45s of no contact"}
		s.mu.Lock()
		s.pendingClose[key] = pendingLeave{url: relayURL, token: token}
		s.mu.Unlock()
	}
	results = append(results, step)
	s.mu.Lock()
	if s.lastEnd != nil {
		s.lastEnd.Results = results
	}
	s.mu.Unlock()
	return results, nil
}

// endLocked tears down everything local. Caller holds s.mu.
func (s *Service) endLocked(reason string) []StepResult {
	sess := s.RoomMgr.CurrentSession()
	var results []StepResult
	if sess != nil && sess.Role == room.RoleRequester {
		if s.provider != nil {
			s.provider.Close()
			results = append(results, StepResult{Step: "Stop sharing your proxy (all partner connections cut)", Success: true})
		} else {
			results = append(results, StepResult{Step: "Stop sharing your proxy", Success: true, Skipped: true})
		}
	}
	if s.consumer != nil {
		s.consumer.Close()
		results = append(results, StepResult{Step: "Close the encrypted tunnel port", Success: true})
	} else if sess != nil && sess.Role == room.RoleOperator {
		results = append(results, StepResult{Step: "Close the encrypted tunnel port", Success: true, Skipped: true})
	}
	if s.Chrome.IsRunning() {
		r := StepResult{Step: "Quit the temporary Chrome and delete its profile", Success: true}
		if err := s.Chrome.Close(); err != nil {
			r.Success, r.Error = false, err.Error()
		}
		results = append(results, r)
	} else if sess != nil && sess.Role == room.RoleOperator {
		results = append(results, StepResult{Step: "Quit the temporary Chrome", Success: true, Skipped: true})
	}
	s.Signaling.DisconnectWS()
	_, _ = s.RoomMgr.CloseRoom()
	s.gen++
	s.provider, s.consumer = nil, nil
	s.token, s.secret, s.invite, s.relayURL = "", tunnel.Secret{}, "", ""
	s.net, s.checking = NetworkView{}, false
	s.lastEnd = &EndInfo{Reason: reason, Results: results, At: time.Now()}
	return results
}

// ensureDataPlaneLocked starts the tunnel side for this role once the room is ACTIVE.
func (s *Service) ensureDataPlaneLocked() {
	sess := s.RoomMgr.CurrentSession()
	if sess == nil || sess.State != room.StateActive {
		return
	}
	if sess.Role == room.RoleRequester && s.provider == nil {
		p, err := tunnel.StartProvider(s.relayURL, sess.RoomKey, s.token, s.secret, sess.ProxyAddress)
		if err != nil {
			s.net.Message = "Cannot start sharing: " + err.Error()
			return
		}
		s.provider = p
		s.net.State = NetSharing
	}
	if sess.Role == room.RoleOperator && s.consumer == nil {
		c, err := tunnel.StartConsumer(s.relayURL, sess.RoomKey, s.token, s.secret)
		if err != nil {
			s.net.State, s.net.Message = NetFailed, "Cannot open local tunnel port: "+err.Error()
			return
		}
		s.consumer = c
		s.net.State, s.net.Message = NetConnecting, "Verifying the host's network exit…"
		s.startCheckLocked()
	}
}

func (s *Service) startCheckLocked() {
	if s.consumer == nil || s.checking {
		return
	}
	s.checking, s.lastCheck = true, time.Now()
	go s.checkNetwork(s.gen, s.consumer.Addr(), s.net.ExpectedExitIP)
}

// checkNetwork measures the exit IP through the tunnel. Chrome is only allowed
// after this succeeds; if it later fails, Chrome stays blocked by design (its
// only route is the tunnel port).
func (s *Service) checkNetwork(gen uint64, addr, expected string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	direct, _ := s.DirectIP(ctx)
	exit, err := s.TunnelProbe.FetchExitIP(ctx, addr)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return
	}
	s.checking, s.lastCheck = false, time.Now()
	s.net.DirectIP, s.net.CheckedAt = direct, time.Now()
	wasVerified := s.net.State == NetVerified
	switch {
	case err != nil:
		s.net.State = NetFailed
		if wasVerified {
			s.net.Message = "Lost the host's network. Browser traffic is blocked until it recovers."
		} else {
			s.net.Message = "Cannot reach the internet through the host: " + shortErr(err)
		}
	default:
		// The tunnel port can only reach the host's proxy, so a successful fetch
		// through it is the proof. IP comparisons are informational.
		s.net.State, s.net.ExitIP, s.net.Message, s.net.Warning = NetVerified, exit, "", ""
		switch {
		case direct != "" && exit == direct:
			s.net.Warning = "The host's exit IP is the same as your own public IP (same network or VPN node?)."
		case expected != "" && expected != exit:
			s.net.Warning = "Exit IP differs from the host's (" + expected + "); their proxy may rotate nodes."
		}
	}
}

// ---- reconciliation ----

func (s *Service) syncOnce() {
	s.mu.Lock()
	pending := make(map[string]pendingLeave, len(s.pendingClose))
	for k, v := range s.pendingClose {
		pending[k] = v
	}
	sess := s.RoomMgr.CurrentSession()
	gen, token := s.gen, s.token
	s.mu.Unlock()

	for key, pl := range pending {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := s.Signaling.LeaveRoomAt(ctx, pl.url, key, pl.token)
		cancel()
		if err == nil || signaling.IsGone(err) {
			s.mu.Lock()
			delete(s.pendingClose, key)
			s.mu.Unlock()
		}
	}
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	rem, err := s.Signaling.GetRoomStatus(ctx, sess.RoomKey, token)
	cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		return // a user operation replaced the session while we were waiting
	}
	now := time.Now()
	switch {
	case signaling.IsGone(err):
		s.endLocked(endReason(sess))
		return
	case err != nil:
		if now.Sub(s.lastOK) > 60*time.Second {
			s.endLocked("Lost contact with the signaling server for over a minute; access was revoked.")
			return
		}
		s.RoomMgr.SetSyncError("Signaling unavailable; retrying")
		s.refreshNetLocked(nil)
		return
	case rem.RoomKey != sess.RoomKey:
		return
	}
	s.lastOK = now
	s.RoomMgr.SetSyncError("")
	s.RoomMgr.SetRemoteMetadata(rem.CreatedAt, rem.ExpiresAt, rem.ExitIP)
	switch rem.State {
	case "PENDING_APPROVAL":
		if sess.State == room.StateWaiting {
			_, _ = s.RoomMgr.TransitionTo(room.StatePendingApproval)
		}
	case "ACTIVE":
		if sess.State == room.StateWaiting {
			_, _ = s.RoomMgr.TransitionTo(room.StatePendingApproval)
		}
		_, _ = s.RoomMgr.TransitionTo(room.StateActive)
		s.ensureDataPlaneLocked()
	}
	s.refreshNetLocked(rem)
}

func endReason(sess *room.Session) string {
	if sess.Role == room.RoleOperator {
		if sess.State == room.StatePendingApproval {
			return "The host declined or closed the room."
		}
		return "The host ended the session or the room expired."
	}
	return "Your partner left or the room expired."
}

func (s *Service) refreshNetLocked(rem *signaling.RemoteRoom) {
	if s.provider != nil {
		online, streams, lastErr := s.provider.Status()
		s.net.RelayOnline, s.net.Streams = online, streams
		if online {
			s.net.Message = ""
		} else if lastErr != "" {
			s.net.Message = "Relay link down, reconnecting (partner traffic is blocked): " + lastErr
		}
	}
	if s.consumer != nil {
		s.net.Streams = s.consumer.Streams()
		if rem != nil {
			s.net.RelayOnline = rem.ProviderOnline
		}
		since := time.Since(s.lastCheck)
		if (s.net.State != NetVerified && since > 4*time.Second) || since > 30*time.Second {
			s.startCheckLocked()
		}
	}
}

// ---- queries ----

func (s *Service) viewLocked() *SessionView {
	sess := s.RoomMgr.CurrentSession()
	if sess == nil {
		return nil
	}
	v := &SessionView{Session: *sess, Invite: s.invite, Network: s.net, ChromeRunning: s.Chrome.IsRunning()}
	if s.relayURL != "" && s.relayURL != s.PublicURL {
		v.Relay = strings.TrimPrefix(strings.TrimPrefix(s.relayURL, "https://"), "http://")
	}
	return v
}

func (s *Service) GetSession() *SessionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewLocked()
}

// LastEnded explains why the previous session ended and what was cleaned up.
func (s *Service) LastEnded() *EndInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastEnd == nil {
		return nil
	}
	e := *s.lastEnd
	return &e
}

func (s *Service) LaunchChrome(url string) error {
	s.mu.Lock()
	sess := s.RoomMgr.CurrentSession()
	if sess == nil || sess.Role != room.RoleOperator || sess.State != room.StateActive {
		s.mu.Unlock()
		return errors.New("browser is only available to an approved participant")
	}
	if s.net.State != NetVerified || s.consumer == nil {
		s.mu.Unlock()
		return errors.New("the host's network is not verified yet; browser stays blocked")
	}
	addr := s.consumer.Addr()
	s.mu.Unlock()
	return s.Chrome.Launch(url, addr)
}

func (s *Service) CloseChrome() error { return s.Chrome.Close() }

func (s *Service) ProbeLocalProxies() []egress.ProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.Probe.DiscoverLocal(ctx)
}

func (s *Service) CheckProxyAddress(address string) egress.ProbeResult {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.Probe.Probe(ctx, address)
}

func (s *Service) cachedPublicIP(ctx context.Context) (string, error) {
	s.statusMu.Lock()
	if s.pubIP != "" && time.Since(s.pubIPAt) < time.Minute {
		ip := s.pubIP
		s.statusMu.Unlock()
		return ip, nil
	}
	s.statusMu.Unlock()
	ip, err := s.NetResolver.GetPublicIP(ctx)
	if err != nil {
		return "", err
	}
	s.statusMu.Lock()
	s.pubIP, s.pubIPAt = ip, time.Now()
	s.statusMu.Unlock()
	return ip, nil
}

func (s *Service) GetSystemStatus() SystemStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ip, _ := s.DirectIP(ctx)
	s.statusMu.Lock()
	if s.cameraAt.IsZero() || time.Since(s.cameraAt) > 5*time.Minute {
		s.camera, s.cameraAt = device.DetectCamera(), time.Now()
	}
	cam := s.camera
	s.statusMu.Unlock()
	return SystemStatus{PublicIP: ip, Camera: cam, ChromeRunning: s.Chrome.IsRunning(), ServerURL: s.Signaling.URL()}
}

// ---- relay choice ----

func (s *Service) RelayConfig() RelayView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RelayView{Mode: s.relayCfg.RelayMode, CustomWorkerURL: s.relayCfg.CustomWorkerURL, PublicAvailable: s.PublicURL != ""}
}

// CheckRelay probes a relay's /health. raw "" checks the relay new rooms use.
func (s *Service) CheckRelay(raw string) RelayHealth {
	target := s.PublicURL
	if strings.TrimSpace(raw) != "" && raw != s.PublicURL {
		u, err := config.NormalizeRelayURL(raw)
		if err != nil {
			return RelayHealth{Error: err.Error()}
		}
		target = u
	} else if v := s.RelayConfig(); v.Mode == config.RelayCustom {
		target = v.CustomWorkerURL
	}
	if target == "" {
		return RelayHealth{Error: "no default relay in this build"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	start := time.Now()
	h, err := s.Signaling.CheckHealth(ctx, target)
	out := RelayHealth{URL: target, LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		out.Error = shortErr(err)
		return out
	}
	out.OK, out.Worker = true, h.Worker
	return out
}

// SetRelay picks the relay for new rooms. A private Worker is saved only after
// its /health answers, so a typo never strands the next room. The current
// session (if any) keeps its relay.
func (s *Service) SetRelay(mode, raw string) (RelayView, RelayHealth, error) {
	cur := s.RelayConfig()
	next := config.Config{RelayMode: config.RelayPublic, CustomWorkerURL: cur.CustomWorkerURL}
	var health RelayHealth
	switch mode {
	case config.RelayPublic:
		if s.PublicURL == "" {
			return cur, RelayHealth{Error: "no default relay in this build"}, errors.New("this build has no default relay")
		}
		health = s.CheckRelay(s.PublicURL)
	case config.RelayCustom:
		u, err := config.NormalizeRelayURL(raw)
		if err != nil {
			return cur, RelayHealth{Error: err.Error()}, err
		}
		health = s.CheckRelay(u)
		if !health.OK {
			return cur, health, errors.New("Worker is offline or not a TempRoom relay: " + health.Error)
		}
		next = config.Config{RelayMode: config.RelayCustom, CustomWorkerURL: u}
	default:
		return cur, health, errors.New("unknown relay mode")
	}
	if err := config.Save(s.configDir, next); err != nil {
		return cur, health, fmt.Errorf("save settings: %w", err)
	}
	s.mu.Lock()
	s.relayCfg = next
	s.mu.Unlock()
	return RelayView{Mode: next.RelayMode, CustomWorkerURL: next.CustomWorkerURL, PublicAvailable: s.PublicURL != ""}, health, nil
}
