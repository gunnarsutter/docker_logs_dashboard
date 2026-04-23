package state

import (
	"sync"
	"testing"
	"time"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestManager(t *testing.T, states []string, events []config.Event) *StateManager {
	t.Helper()
	cfgStates := make([]config.State, len(states))
	for i, s := range states {
		cfgStates[i] = config.State{Name: s}
	}
	sm, err := NewStateManager(cfgStates, events)
	if err != nil {
		t.Fatalf("NewStateManager: %v", err)
	}
	return sm
}

func entry(msg string) docker.LogEntry {
	return docker.LogEntry{ContainerName: "test", Message: msg}
}

// ── NewStateManager ───────────────────────────────────────────────────────────

func TestNewStateManager_InvalidPattern(t *testing.T) {
	_, err := NewStateManager(
		[]config.State{{Name: "ok"}},
		[]config.Event{{Name: "e", Pattern: `[invalid`, State: "ok"}},
	)
	if err == nil {
		t.Fatal("expected error for invalid event regex, got nil")
	}
}

func TestNewStateManager_DefaultState(t *testing.T) {
	sm := newTestManager(t, []string{"healthy"}, nil)
	if got := sm.GetCurrentState(); got != "healthy" {
		t.Errorf("expected default state 'healthy', got %q", got)
	}
}

// ── ProcessLogEntry ───────────────────────────────────────────────────────────

func TestProcessLogEntry_StateTransition(t *testing.T) {
	sm := newTestManager(t, []string{"healthy", "degraded"}, []config.Event{
		{Name: "error-event", Pattern: `ERROR`, State: "degraded"},
	})

	sm.ProcessLogEntry(entry("all is well"))
	if sm.GetCurrentState() != "healthy" {
		t.Error("state should not change for non-matching log")
	}

	sm.ProcessLogEntry(entry("ERROR: connection refused"))
	if sm.GetCurrentState() != "degraded" {
		t.Errorf("expected state 'degraded', got %q", sm.GetCurrentState())
	}
}

func TestProcessLogEntry_FirstMatchWins(t *testing.T) {
	sm := newTestManager(t, []string{"healthy", "degraded", "critical"}, []config.Event{
		{Name: "warn", Pattern: `WARN`, State: "degraded"},
		{Name: "err", Pattern: `WARN`, State: "critical"}, // same pattern, second event
	})

	sm.ProcessLogEntry(entry("WARN: something wrong"))
	if sm.GetCurrentState() != "degraded" {
		t.Errorf("expected first matching event to win, got %q", sm.GetCurrentState())
	}
}

func TestProcessLogEntry_NoTransitionWhenAlreadyInTargetState(t *testing.T) {
	var listenerCalls int
	sm := newTestManager(t, []string{"degraded"}, []config.Event{
		{Name: "err", Pattern: `ERROR`, State: "degraded"},
	})
	sm.AddStateChangeListener(func(from, to, event, container string, ts time.Time) {
		listenerCalls++
	})

	sm.ProcessLogEntry(entry("ERROR: first"))
	time.Sleep(20 * time.Millisecond) // let listener goroutine run

	sm.ProcessLogEntry(entry("ERROR: second"))
	time.Sleep(20 * time.Millisecond)

	// Listener should only fire on actual transitions (healthy→degraded), not same→same
	if listenerCalls != 1 {
		t.Errorf("expected 1 listener call (only on transition), got %d", listenerCalls)
	}
}

func TestProcessLogEntry_NoMatch(t *testing.T) {
	sm := newTestManager(t, []string{"healthy", "degraded"}, []config.Event{
		{Name: "err", Pattern: `ERROR`, State: "degraded"},
	})
	sm.ProcessLogEntry(entry("INFO: all good"))
	if sm.GetCurrentState() != "healthy" {
		t.Errorf("state should remain 'healthy', got %q", sm.GetCurrentState())
	}
}

// ── StateChangeListener ───────────────────────────────────────────────────────

func TestStateChangeListener_CalledOnTransition(t *testing.T) {
	sm := newTestManager(t, []string{"healthy", "degraded"}, []config.Event{
		{Name: "err", Pattern: `ERROR`, State: "degraded"},
	})

	var mu sync.Mutex
	var got struct {
		from, to, event, container string
	}
	sm.AddStateChangeListener(func(from, to, event, container string, ts time.Time) {
		mu.Lock()
		got.from = from
		got.to = to
		got.event = event
		got.container = container
		mu.Unlock()
	})

	sm.ProcessLogEntry(entry("ERROR: boom"))
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if got.from != "healthy" {
		t.Errorf("expected from='healthy', got %q", got.from)
	}
	if got.to != "degraded" {
		t.Errorf("expected to='degraded', got %q", got.to)
	}
	if got.event != "err" {
		t.Errorf("expected event='err', got %q", got.event)
	}
	if got.container != "test" {
		t.Errorf("expected container='test', got %q", got.container)
	}
}

func TestStateChangeListener_MultipleListeners(t *testing.T) {
	sm := newTestManager(t, []string{"healthy", "degraded"}, []config.Event{
		{Name: "err", Pattern: `ERROR`, State: "degraded"},
	})

	var mu sync.Mutex
	calls := 0
	for i := 0; i < 3; i++ {
		sm.AddStateChangeListener(func(from, to, event, container string, ts time.Time) {
			mu.Lock()
			calls++
			mu.Unlock()
		})
	}

	sm.ProcessLogEntry(entry("ERROR: three listeners"))
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("expected 3 listener calls, got %d", calls)
	}
}

// ── GetStateDescription ───────────────────────────────────────────────────────

func TestGetStateDescription(t *testing.T) {
	cfgStates := []config.State{
		{Name: "healthy", Description: "All systems operational"},
		{Name: "degraded", Description: "Partial outage"},
	}
	sm, err := NewStateManager(cfgStates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sm.GetStateDescription("healthy"); got != "All systems operational" {
		t.Errorf("unexpected description: %q", got)
	}
	if got := sm.GetStateDescription("unknown"); got != "" {
		t.Errorf("expected empty description for unknown state, got %q", got)
	}
}
