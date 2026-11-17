package room

import (
	"testing"
)

func TestRoomLifecycle(t *testing.T) {
	mgr := NewManager()

	// 1. Create Room
	s := mgr.CreateRoomWithKey("TEST-2345", RoleRequester)
	if s == nil {
		t.Fatal("expected session, got nil")
	}
	if s.Role != RoleRequester {
		t.Fatalf("expected RoleRequester, got %v", s.Role)
	}
	if s.State != StateWaiting {
		t.Fatalf("expected StateWaiting, got %v", s.State)
	}
	if s.RoomKey == "" {
		t.Fatal("expected non-empty room key")
	}

	// 2. Transition State
	s2, err := mgr.TransitionTo(StatePreparing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s2.State != StatePreparing {
		t.Fatalf("expected StatePreparing, got %v", s2.State)
	}

	// 3. Close Room
	s3, err := mgr.CloseRoom()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if s3.State != StateClosed {
		t.Fatalf("expected StateClosed, got %v", s3.State)
	}

	// 4. Verify CurrentSession is nil
	if mgr.CurrentSession() != nil {
		t.Fatal("expected current session to be nil after close")
	}
}

func TestJoinRoom(t *testing.T) {
	mgr := NewManager()
	s, err := mgr.JoinRoom("TEST-1234", RoleOperator)
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}
	if s.State != StatePendingApproval {
		t.Fatalf("expected StatePendingApproval, got %v", s.State)
	}
}

func TestSnapshotsAndTransitions(t *testing.T) {
	m := NewManager()
	r := m.CreateRoomWithKey("ABCD-2345", RoleRequester)
	r.State = StateActive
	if m.CurrentSession().State != StateWaiting {
		t.Fatal("mutable snapshot")
	}
	if _, err := m.TransitionTo(StateActive); err != ErrInvalidState {
		t.Fatal("skipped approval")
	}
	m.TransitionTo(StatePendingApproval)
	m.TransitionTo(StateActive)
	if _, err := m.TransitionTo(StatePendingApproval); err != ErrInvalidState {
		t.Fatal("state regressed")
	}
}
