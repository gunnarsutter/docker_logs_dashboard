package status

import (
	"sync"
	"time"
)

// ServiceStatus represents the status of a monitored service
type ServiceStatus struct {
	Name           string
	Running        bool
	LastSeen       time.Time
	CustomStatuses map[string]StatusItem
	Mu             sync.RWMutex
}

// StatusItem represents a custom status item
type StatusItem struct {
	Label     string
	Status    string // "ok", "pending", "error", "unknown"
	Value     string
	UpdatedAt time.Time
}

// Tracker tracks status of multiple services
type Tracker struct {
	services map[string]*ServiceStatus
	mu       sync.RWMutex
}

// NewTracker creates a new status tracker
func NewTracker() *Tracker {
	return &Tracker{
		services: make(map[string]*ServiceStatus),
	}
}

// RegisterService registers a new service for tracking
func (t *Tracker) RegisterService(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.services[name]; !exists {
		t.services[name] = &ServiceStatus{
			Name:           name,
			Running:        false,
			CustomStatuses: make(map[string]StatusItem),
		}
	}
}

// UpdateServiceRunning updates whether a service is running
func (t *Tracker) UpdateServiceRunning(serviceName string, running bool) {
	t.mu.RLock()
	service, exists := t.services[serviceName]
	t.mu.RUnlock()

	if !exists {
		return
	}

	service.Mu.Lock()
	defer service.Mu.Unlock()
	service.Running = running
	if running {
		service.LastSeen = time.Now()
	}
}

// UpdateCustomStatus updates a custom status item for a service
func (t *Tracker) UpdateCustomStatus(serviceName, key, label, status, value string) {
	t.mu.RLock()
	service, exists := t.services[serviceName]
	t.mu.RUnlock()

	if !exists {
		return
	}

	service.Mu.Lock()
	defer service.Mu.Unlock()
	service.CustomStatuses[key] = StatusItem{
		Label:     label,
		Status:    status,
		Value:     value,
		UpdatedAt: time.Now(),
	}
}

// GetServiceStatus gets the status of a service
func (t *Tracker) GetServiceStatus(serviceName string) (*ServiceStatus, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	service, exists := t.services[serviceName]
	return service, exists
}

// GetAllServices returns all tracked services
func (t *Tracker) GetAllServices() map[string]*ServiceStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]*ServiceStatus)
	for name, service := range t.services {
		result[name] = service
	}
	return result
}

// GetCustomStatus gets a specific custom status
func (s *ServiceStatus) GetCustomStatus(key string) (StatusItem, bool) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	item, exists := s.CustomStatuses[key]
	return item, exists
}

// IsRunning returns whether the service is running
func (s *ServiceStatus) IsRunning() bool {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.Running
}
