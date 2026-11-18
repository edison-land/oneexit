package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ProtocolVersion lets clients detect an outdated deployment.
const ProtocolVersion = 2

type RoomState string

const (
	StateWaiting         RoomState = "WAITING"
	StatePendingApproval RoomState = "PENDING_APPROVAL"
	StateActive          RoomState = "ACTIVE"
)

var (
	// Unapproved rooms expire after 30 minutes; approved rooms stay alive as
	// long as peers keep polling (sliding 24h window, 45s heartbeat lease).
	WaitingTTL = 30 * time.Minute
	ActiveTTL  = 24 * time.Hour
	// A side that stops polling for this long is treated as gone (crash, quit, lost network).
	LeaseTTL      = 45 * time.Second
	sweepInterval = 5 * time.Second
	streamWait    = 10 * time.Second
)

const maxStreamsPerRoom = 256

var (
	errNotFound  = errors.New("room not found or expired")
	errConflict  = errors.New("invalid room state")
	errForbidden = errors.New("invalid room token")
	errOccupied  = errors.New("room already has an operator")
	errOffline   = errors.New("host is not connected to the relay")
)

type hintConn struct{ sendCh chan []byte }

type pendingStream struct {
	key    string
	accept chan net.Conn
}

type Room struct {
	ID             string    `json:"id"`
	RoomKey        string    `json:"roomKey"`
	State          RoomState `json:"state"`
	ExitIP         string    `json:"exitIp,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	ProviderOnline bool      `json:"providerOnline"`
	OperatorJoined bool      `json:"operatorJoined"`
	Token          string    `json:"token,omitempty"` // only in create/join responses
	Role           string    `json:"role,omitempty"`

	requesterToken string
	operatorToken  string
	joinID         string
	createID       string
	seen           map[string]time.Time
	hints          map[string]*hintConn
	provider       chan string
	providerConn   *websocket.Conn
	conns          map[io.Closer]struct{}
}

type Hub struct {
	stop    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	rooms   map[string]*Room
	creates map[string]string // create request ID -> room key
	pending map[string]*pendingStream
}

func NewHub() *Hub {
	h := &Hub{rooms: map[string]*Room{}, creates: map[string]string{}, pending: map[string]*pendingStream{}, stop: make(chan struct{})}
	go h.sweeper()
	return h
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func generateKey() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return s[:4] + "-" + s[4:]
}

func normKey(k string) string { return strings.ToUpper(strings.TrimSpace(k)) }

func snapshot(r *Room) *Room {
	return &Room{ID: r.ID, RoomKey: r.RoomKey, State: r.State, ExitIP: r.ExitIP, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		ProviderOnline: r.providerConn != nil, OperatorJoined: r.operatorToken != ""}
}

func (h *Hub) liveRoom(key string) *Room {
	r := h.rooms[normKey(key)]
	if r == nil || !time.Now().Before(r.ExpiresAt) {
		return nil
	}
	return r
}

// auth returns the caller's role if the token belongs to the room. Caller holds h.mu.
func (r *Room) auth(token string) string {
	if token == "" {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(r.requesterToken)) == 1 {
		return "REQUESTER"
	}
	if r.operatorToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(r.operatorToken)) == 1 {
		return "OPERATOR"
	}
	return ""
}

func (r *Room) hint(event string) {
	msg, _ := json.Marshal(map[string]string{"event": event, "roomKey": r.RoomKey})
	for _, c := range r.hints {
		select {
		case c.sendCh <- msg:
		default: // polling recovers dropped hints
		}
	}
}

func (h *Hub) Create(exitIP, requestID string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if requestID != "" {
		if key, ok := h.creates[requestID]; ok {
			if r := h.liveRoom(key); r != nil && r.State == StateWaiting {
				s := snapshot(r)
				s.Token, s.Role = r.requesterToken, "REQUESTER"
				return s, nil
			}
		}
	}
	key := generateKey()
	for h.rooms[key] != nil {
		key = generateKey()
	}
	now := time.Now()
	r := &Room{ID: "room_" + strings.ToLower(key), RoomKey: key, State: StateWaiting, ExitIP: exitIP, CreatedAt: now, ExpiresAt: now.Add(WaitingTTL),
		requesterToken: randHex(24), createID: requestID, seen: map[string]time.Time{"REQUESTER": now},
		hints: map[string]*hintConn{}, provider: make(chan string, 64), conns: map[io.Closer]struct{}{}}
	h.rooms[key] = r
	if requestID != "" {
		h.creates[requestID] = key
	}
	s := snapshot(r)
	s.Token, s.Role = r.requesterToken, "REQUESTER"
	return s, nil
}

// Join is idempotent per joinID: the same B can retry; anyone else gets 409.
func (h *Hub) Join(key, joinID string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.liveRoom(key)
	if r == nil {
		return nil, errNotFound
	}
	if joinID == "" {
		return nil, errConflict
	}
	if r.operatorToken != "" {
		if subtle.ConstantTimeCompare([]byte(joinID), []byte(r.joinID)) != 1 {
			return nil, errOccupied
		}
	} else {
		if r.State != StateWaiting {
			return nil, errConflict
		}
		r.operatorToken, r.joinID, r.State = randHex(24), joinID, StatePendingApproval
		r.hint("join_request")
	}
	r.seen["OPERATOR"] = time.Now()
	s := snapshot(r)
	s.Token, s.Role = r.operatorToken, "OPERATOR"
	return s, nil
}

func (h *Hub) Status(key, token string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.liveRoom(key)
	if r == nil {
		return nil, errNotFound
	}
	role := r.auth(token)
	if role == "" {
		return nil, errForbidden
	}
	now := time.Now()
	r.seen[role] = now
	if r.State == StateActive {
		r.ExpiresAt = now.Add(ActiveTTL)
	}
	s := snapshot(r)
	s.Role = role
	return s, nil
}

func (h *Hub) Approve(key, token string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.liveRoom(key)
	if r == nil {
		return nil, errNotFound
	}
	if r.auth(token) != "REQUESTER" {
		return nil, errForbidden
	}
	switch r.State {
	case StatePendingApproval:
		r.State = StateActive
		r.ExpiresAt = time.Now().Add(ActiveTTL)
		r.hint("join_approved")
	case StateActive: // idempotent retry
	default:
		return nil, errConflict
	}
	r.seen["REQUESTER"] = time.Now()
	return snapshot(r), nil
}

func (h *Hub) Leave(key, token string) error {
	h.mu.Lock()
	r := h.liveRoom(key)
	if r == nil {
		h.mu.Unlock()
		return errNotFound
	}
	if r.auth(token) == "" {
		h.mu.Unlock()
		return errForbidden
	}
	closers := h.removeLocked(r)
	h.mu.Unlock()
	closeAll(closers)
	return nil
}

// removeLocked deletes the room and returns everything that must be closed.
func (h *Hub) removeLocked(r *Room) []io.Closer {
	r.hint("room_closed")
	delete(h.rooms, r.RoomKey)
	if r.createID != "" {
		delete(h.creates, r.createID)
	}
	for id, p := range h.pending {
		if p.key == r.RoomKey {
			delete(h.pending, id)
		}
	}
	var out []io.Closer
	for c := range r.conns {
		out = append(out, c)
	}
	r.conns = map[io.Closer]struct{}{}
	if r.providerConn != nil {
		pc := r.providerConn
		out = append(out, closerFunc(func() error { return pc.CloseNow() }))
		r.providerConn = nil
	}
	return out
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func closeAll(cs []io.Closer) {
	for _, c := range cs {
		_ = c.Close()
	}
}

func (h *Hub) sweeper() {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
		}
		h.sweep()
	}
}

func (h *Hub) sweep() {
	now := time.Now()
	var closers []io.Closer
	h.mu.Lock()
	for _, r := range h.rooms {
		expired := !now.Before(r.ExpiresAt)
		for role, at := range r.seen {
			if role == "OPERATOR" && r.operatorToken == "" {
				continue
			}
			if now.Sub(at) > LeaseTTL {
				expired = true
			}
		}
		if expired {
			closers = append(closers, h.removeLocked(r)...)
		}
	}
	h.mu.Unlock()
	closeAll(closers)
}

type Server struct{ hub *Hub }

func NewServer() *Server { return &Server{hub: NewHub()} }
func (s *Server) Close() { s.hub.once.Do(func() { close(s.hub.stop) }) }
func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func statusCode(err error) int {
	switch {
	case errors.Is(err, errForbidden):
		return http.StatusForbidden
	case errors.Is(err, errConflict), errors.Is(err, errOccupied):
		return http.StatusConflict
	case errors.Is(err, errOffline):
		return http.StatusServiceUnavailable
	}
	return http.StatusNotFound
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ws", s.handleHints)
	mux.HandleFunc("/ws/relay/provider", s.handleProvider)
	mux.HandleFunc("/ws/relay/connect", s.handleConnect)
	mux.HandleFunc("/ws/relay/accept", s.handleAccept)
	mux.HandleFunc("/api/v1/rooms/create", s.post(s.handleCreate))
	mux.HandleFunc("/api/v1/rooms/join", s.post(s.handleJoin))
	mux.HandleFunc("/api/v1/rooms/approve", s.post(s.handleApprove))
	mux.HandleFunc("/api/v1/rooms/leave", s.post(s.handleLeave))
	mux.HandleFunc("/api/v1/rooms/status", s.handleStatus)
	return mux
}

type roomReq struct {
	RoomKey   string `json:"roomKey"`
	ExitIP    string `json:"exitIp"`
	RequestID string `json:"requestId"`
	JoinID    string `json:"joinId"`
}

func (s *Server) post(fn func(http.ResponseWriter, *http.Request, roomReq)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req roomReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		fn(w, r, req)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.hub.mu.Lock()
	n := len(s.hub.rooms)
	s.hub.mu.Unlock()
	writeJSON(w, map[string]any{"status": "ok", "protocol": ProtocolVersion, "activeRooms": n, "time": time.Now().Format(time.RFC3339)})
}

func (s *Server) handleCreate(w http.ResponseWriter, _ *http.Request, req roomReq) {
	if len(req.ExitIP) > 64 || len(req.RequestID) > 64 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	room, err := s.hub.Create(req.ExitIP, req.RequestID)
	if err != nil {
		http.Error(w, "room allocation failed", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, room)
}

func (s *Server) handleJoin(w http.ResponseWriter, _ *http.Request, req roomReq) {
	if len(req.JoinID) < 16 || len(req.JoinID) > 64 {
		http.Error(w, "invalid join id", http.StatusBadRequest)
		return
	}
	room, err := s.hub.Join(req.RoomKey, req.JoinID)
	if err != nil {
		http.Error(w, err.Error(), statusCode(err))
		return
	}
	writeJSON(w, room)
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request, req roomReq) {
	room, err := s.hub.Approve(req.RoomKey, bearer(r))
	if err != nil {
		http.Error(w, err.Error(), statusCode(err))
		return
	}
	writeJSON(w, room)
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request, req roomReq) {
	if err := s.hub.Leave(req.RoomKey, bearer(r)); err != nil {
		http.Error(w, err.Error(), statusCode(err))
		return
	}
	writeJSON(w, map[string]any{"success": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	room, err := s.hub.Status(r.URL.Query().Get("key"), bearer(r))
	if err != nil {
		http.Error(w, err.Error(), statusCode(err))
		return
	}
	writeJSON(w, room)
}

// authWS validates before upgrading so failures are plain HTTP errors.
func (s *Server) authWS(w http.ResponseWriter, r *http.Request, wantRole string, needActive bool) (*Room, string, bool) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	room := s.hub.liveRoom(r.URL.Query().Get("key"))
	if room == nil {
		http.Error(w, errNotFound.Error(), http.StatusNotFound)
		return nil, "", false
	}
	role := room.auth(bearer(r))
	if role == "" || (wantRole != "" && role != wantRole) {
		http.Error(w, errForbidden.Error(), http.StatusForbidden)
		return nil, "", false
	}
	if needActive && room.State != StateActive {
		http.Error(w, errConflict.Error(), http.StatusConflict)
		return nil, "", false
	}
	return room, role, true
}

// track registers a connection for revocation; false means the room is gone.
func (h *Hub) track(r *Room, c io.Closer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[r.RoomKey] != r {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

func (h *Hub) untrack(r *Room, c io.Closer) {
	h.mu.Lock()
	delete(r.conns, c)
	h.mu.Unlock()
	_ = c.Close()
}

// handleHints: WebSocket events are invalidation hints only; REST owns all state.
func (s *Server) handleHints(w http.ResponseWriter, r *http.Request) {
	room, role, ok := s.authWS(w, r, "", false)
	if !ok {
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	hc := &hintConn{sendCh: make(chan []byte, 16)}
	s.hub.mu.Lock()
	if s.hub.rooms[room.RoomKey] != room {
		s.hub.mu.Unlock()
		return
	}
	room.hints[role] = hc
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		if room.hints[role] == hc {
			delete(room.hints, role)
		}
		s.hub.mu.Unlock()
	}()
	go func() { // drain reads so pings/close frames are processed
		for {
			if _, _, err := c.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-hc.sendCh:
			wctx, done := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			done()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, done := context.WithTimeout(ctx, 5*time.Second)
			err := c.Ping(pctx)
			done()
			if err != nil {
				return
			}
		}
	}
}

// handleProvider is A's control link. Stream requests are sent as {"stream":id}.
func (s *Server) handleProvider(w http.ResponseWriter, r *http.Request) {
	room, _, ok := s.authWS(w, r, "REQUESTER", true)
	if !ok {
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	s.hub.mu.Lock()
	if s.hub.rooms[room.RoomKey] != room {
		s.hub.mu.Unlock()
		return
	}
	old := room.providerConn
	room.providerConn = c
	s.hub.mu.Unlock()
	if old != nil {
		_ = old.CloseNow()
	}
	defer func() {
		s.hub.mu.Lock()
		if room.providerConn == c {
			room.providerConn = nil
		}
		s.hub.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-room.provider:
			msg, _ := json.Marshal(map[string]string{"stream": id})
			wctx, done := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			done()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, done := context.WithTimeout(ctx, 5*time.Second)
			err := c.Ping(pctx)
			done()
			if err != nil {
				return
			}
		}
	}
}

// handleConnect is one stream from B. It waits for A to dial back, then pipes
// ciphertext; the relay never sees plaintext.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	room, _, ok := s.authWS(w, r, "OPERATOR", true)
	if !ok {
		return
	}
	id := randHex(16)
	ps := &pendingStream{key: room.RoomKey, accept: make(chan net.Conn, 1)}
	s.hub.mu.Lock()
	if room.providerConn == nil {
		s.hub.mu.Unlock()
		http.Error(w, errOffline.Error(), http.StatusServiceUnavailable)
		return
	}
	if len(room.conns) >= maxStreamsPerRoom*2 {
		s.hub.mu.Unlock()
		http.Error(w, "too many streams", http.StatusTooManyRequests)
		return
	}
	s.hub.pending[id] = ps
	s.hub.mu.Unlock()
	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.pending, id)
		s.hub.mu.Unlock()
	}()

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	b := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	if !s.hub.track(room, b) {
		_ = b.Close()
		return
	}
	defer s.hub.untrack(room, b)
	select {
	case room.provider <- id:
	default:
		return
	}
	var a net.Conn
	select {
	case a = <-ps.accept:
	case <-time.After(streamWait):
		s.hub.mu.Lock()
		_, unclaimed := s.hub.pending[id]
		delete(s.hub.pending, id)
		s.hub.mu.Unlock()
		if unclaimed {
			return
		}
		a = <-ps.accept // accept already claimed it and always sends (possibly nil)
	}
	if a == nil {
		return
	}
	defer s.hub.untrack(room, a)
	pipe(a, b)
}

func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request) {
	room, _, ok := s.authWS(w, r, "REQUESTER", true)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	s.hub.mu.Lock()
	ps := s.hub.pending[id]
	if ps != nil && ps.key == room.RoomKey {
		delete(s.hub.pending, id)
	} else {
		ps = nil
	}
	s.hub.mu.Unlock()
	if ps == nil {
		http.Error(w, "unknown stream", http.StatusNotFound)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		ps.accept <- nil
		return
	}
	c.SetReadLimit(1 << 20)
	done := make(chan struct{})
	a := &notifyConn{Conn: websocket.NetConn(context.Background(), c, websocket.MessageBinary), done: done}
	if !s.hub.track(room, a) {
		_ = a.Close()
		ps.accept <- nil
		return
	}
	ps.accept <- a
	<-done // keep the handler alive while the connect side pipes; closed on any teardown
}

type notifyConn struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (n *notifyConn) Close() error {
	err := n.Conn.Close()
	n.once.Do(func() { close(n.done) })
	return err
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// NewHubForTest exposes the server's hub to tests in other packages.
func NewHubForTest(s *Server) *Hub { return s.hub }
