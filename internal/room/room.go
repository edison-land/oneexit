package room

import (
	"errors"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateCreated         State = "CREATED"
	StateWaiting         State = "WAITING"
	StatePendingApproval State = "PENDING_APPROVAL"
	StatePreparing       State = "PREPARING"
	StateConnecting      State = "CONNECTING"
	StateActive          State = "ACTIVE"
	StateInterrupted     State = "INTERRUPTED"
	StateClosing         State = "CLOSING"
	StateClosed          State = "CLOSED"
	StateExpired         State = "EXPIRED"
)

type Role string

const (
	RoleRequester Role = "REQUESTER" // A: Provides network
	RoleOperator  Role = "OPERATOR"  // B: Executes browser task
)

var (
	ErrRoomNotFound = errors.New("room not found")
	ErrRoomClosed   = errors.New("room closed")
	ErrRoomExpired  = errors.New("room expired")
	ErrInvalidState = errors.New("invalid state transition")
	ErrUnauthorized = errors.New("unauthorized action")
)

type Session struct {
	SyncError    string    `json:"syncError,omitempty"`
	ID           string    `json:"id"`
	RoomKey      string    `json:"roomKey"`
	Role         Role      `json:"role"`
	State        State     `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	PeerOnline   bool      `json:"peerOnline"`
	ExitIP       string    `json:"exitIp,omitempty"`
	ProxyAddress string    `json:"proxyAddress,omitempty"`
}

type Manager struct {
	mu      sync.RWMutex
	current *Session
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) CreateRoomWithKey(key string, role Role) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	key = strings.TrimSpace(strings.ToUpper(key))
	now := time.Now()
	s := &Session{
		ID:        "room_" + strings.ToLower(key),
		RoomKey:   key,
		Role:      role,
		State:     StateWaiting,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	}
	m.current = s
	return clone(s)
}

func (m *Manager) JoinRoom(key string, role Role) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key = strings.TrimSpace(strings.ToUpper(key))
	if key == "" {
		return nil, errors.New("empty room key")
	}

	now := time.Now()
	s := &Session{
		ID:        "room_" + strings.ToLower(key),
		RoomKey:   key,
		Role:      role,
		State:     StatePendingApproval,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	}
	m.current = s
	return clone(s), nil
}

func (m *Manager) CurrentSession() *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.current)
}

func (m *Manager) TransitionTo(target State) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return nil, ErrRoomNotFound
	}
	if m.current.State == StateClosed || m.current.State == StateExpired {
		return nil, ErrRoomClosed
	}

	if target != m.current.State && !allowed(m.current.State, target) {
		return nil, ErrInvalidState
	}
	m.current.State = target
	return clone(m.current), nil
}

func (m *Manager) SetPeerOnline(online bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.PeerOnline = online
	}
}

func (m *Manager) SetExitIP(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.ExitIP = ip
	}
}

func (m *Manager) SetProxyAddress(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.ProxyAddress = addr
	}
}

func (m *Manager) CloseRoom() (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == nil {
		return nil, ErrRoomNotFound
	}

	m.current.State = StateClosed
	s := m.current
	m.current = nil
	return clone(s), nil
}

func clone(s *Session) *Session {
	if s == nil {
		return nil
	}
	copy := *s
	return &copy
}

func allowed(from, to State) bool {
	if to == StateClosing || to == StateClosed || to == StateExpired {
		return true
	}
	switch from {
	case StateWaiting:
		return to == StatePendingApproval || to == StatePreparing
	case StatePendingApproval:
		return to == StateConnecting || to == StateActive
	case StatePreparing:
		return to == StateWaiting || to == StateConnecting
	case StateConnecting:
		return to == StateActive || to == StateInterrupted
	case StateActive:
		return to == StateInterrupted
	case StateInterrupted:
		return to == StateConnecting
	}
	return false
}

func (m *Manager) SetRemoteMetadata(created, expires time.Time, ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.CreatedAt, m.current.ExpiresAt, m.current.ExitIP = created, expires, ip
	}
}

func (m *Manager) SetSyncError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.SyncError = message
	}
}
