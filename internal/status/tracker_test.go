package status

import (
	"sync"
	"testing"
)

// ── RegisterService / GetServiceStatus ───────────────────────────────────────

func TestRegisterService_NewService(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	svc, ok := tracker.GetServiceStatus("svc")
	if !ok || svc == nil {
		t.Fatal("expected service to be registered")
	}
	if svc.Name != "svc" {
		t.Errorf("unexpected name: %q", svc.Name)
	}
	if svc.IsRunning() {
		t.Error("new service should default to not running")
	}
}

func TestRegisterService_Idempotent(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateServiceRunning("svc", true)
	// Second registration should not reset the service
	tracker.RegisterService("svc")
	svc, _ := tracker.GetServiceStatus("svc")
	if !svc.IsRunning() {
		t.Error("idempotent RegisterService should not overwrite existing service")
	}
}

func TestGetServiceStatus_NotFound(t *testing.T) {
	tracker := NewTracker()
	_, ok := tracker.GetServiceStatus("unknown")
	if ok {
		t.Fatal("expected not found for unregistered service")
	}
}

// ── UpdateServiceRunning ──────────────────────────────────────────────────────

func TestUpdateServiceRunning_True(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateServiceRunning("svc", true)
	svc, _ := tracker.GetServiceStatus("svc")
	if !svc.IsRunning() {
		t.Error("expected service to be running")
	}
	if svc.LastSeen.IsZero() {
		t.Error("expected LastSeen to be set when running=true")
	}
}

func TestUpdateServiceRunning_False(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateServiceRunning("svc", true)
	tracker.UpdateServiceRunning("svc", false)
	svc, _ := tracker.GetServiceStatus("svc")
	if svc.IsRunning() {
		t.Error("expected service to be stopped")
	}
}

func TestUpdateServiceRunning_UnknownService_NoOp(t *testing.T) {
	// Should not panic for unknown service
	tracker := NewTracker()
	tracker.UpdateServiceRunning("ghost", true)
}

// ── UpdateCustomStatus / GetCustomStatus ──────────────────────────────────────

func TestUpdateCustomStatus(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateCustomStatus("svc", "conn", "Connection", "ok", "Connected")

	svc, _ := tracker.GetServiceStatus("svc")
	item, ok := svc.GetCustomStatus("conn")
	if !ok {
		t.Fatal("expected custom status 'conn' to exist")
	}
	if item.Label != "Connection" {
		t.Errorf("unexpected label: %q", item.Label)
	}
	if item.Status != "ok" {
		t.Errorf("unexpected status: %q", item.Status)
	}
	if item.Value != "Connected" {
		t.Errorf("unexpected value: %q", item.Value)
	}
	if item.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestUpdateCustomStatus_Overwrite(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateCustomStatus("svc", "conn", "Connection", "ok", "Connected")
	tracker.UpdateCustomStatus("svc", "conn", "Connection", "error", "Disconnected")

	svc, _ := tracker.GetServiceStatus("svc")
	item, _ := svc.GetCustomStatus("conn")
	if item.Status != "error" || item.Value != "Disconnected" {
		t.Errorf("expected overwritten value, got status=%q value=%q", item.Status, item.Value)
	}
}

func TestUpdateCustomStatus_MultipleKeys(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	tracker.UpdateCustomStatus("svc", "conn", "Connection", "ok", "up")
	tracker.UpdateCustomStatus("svc", "disk", "Disk", "warn", "80%")

	svc, _ := tracker.GetServiceStatus("svc")
	if _, ok := svc.GetCustomStatus("conn"); !ok {
		t.Error("expected 'conn' status")
	}
	if _, ok := svc.GetCustomStatus("disk"); !ok {
		t.Error("expected 'disk' status")
	}
}

func TestUpdateCustomStatus_UnknownService_NoOp(t *testing.T) {
	tracker := NewTracker()
	tracker.UpdateCustomStatus("ghost", "k", "L", "ok", "v") // must not panic
}

func TestGetCustomStatus_NotFound(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")
	svc, _ := tracker.GetServiceStatus("svc")
	_, ok := svc.GetCustomStatus("nonexistent")
	if ok {
		t.Fatal("expected custom status not found")
	}
}

// ── GetAllServices ────────────────────────────────────────────────────────────

func TestGetAllServices(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("a")
	tracker.RegisterService("b")
	all := tracker.GetAllServices()
	if len(all) != 2 {
		t.Errorf("expected 2 services, got %d", len(all))
	}
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestTracker_Concurrent(t *testing.T) {
	tracker := NewTracker()
	tracker.RegisterService("svc")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker.UpdateServiceRunning("svc", i%2 == 0)
			tracker.UpdateCustomStatus("svc", "k", "L", "ok", "v")
			tracker.GetServiceStatus("svc")
			tracker.GetAllServices()
		}(i)
	}
	wg.Wait()
}
