package state

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
)

// StateManager manages system states and events
type StateManager struct {
	currentState string
	states       map[string]config.State
	events       []Event
	listeners    []StateChangeListener
	mu           sync.RWMutex
}

// Event represents a compiled event with its pattern
type Event struct {
	Name    string
	Pattern *regexp.Regexp
	State   string
}

// StateChangeListener is called when state changes
type StateChangeListener func(from, to string, event string, containerName string, timestamp time.Time)

// NewStateManager creates a new state manager
func NewStateManager(states []config.State, events []config.Event) (*StateManager, error) {
	sm := &StateManager{
		currentState: "healthy", // Default state
		states:       make(map[string]config.State),
		events:       make([]Event, 0, len(events)),
		listeners:    make([]StateChangeListener, 0),
	}

	// Build state map
	for _, state := range states {
		sm.states[state.Name] = state
	}

	// Compile event patterns
	for _, eventCfg := range events {
		pattern, err := regexp.Compile(eventCfg.Pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to compile pattern for event '%s': %w", eventCfg.Name, err)
		}

		sm.events = append(sm.events, Event{
			Name:    eventCfg.Name,
			Pattern: pattern,
			State:   eventCfg.State,
		})
	}

	return sm, nil
}

// ProcessLogEntry processes a log entry and checks for state-triggering events
func (sm *StateManager) ProcessLogEntry(entry docker.LogEntry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, event := range sm.events {
		if event.Pattern.MatchString(entry.Message) {
			oldState := sm.currentState
			if oldState != event.State {
				sm.currentState = event.State
				timestamp := time.Now()

				// Notify listeners
				for _, listener := range sm.listeners {
					go listener(oldState, event.State, event.Name, entry.ContainerName, timestamp)
				}
			}
			// Take first matching event only
			return
		}
	}
}

// GetCurrentState returns the current state
func (sm *StateManager) GetCurrentState() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState
}

// GetStateDescription returns the description of a state
func (sm *StateManager) GetStateDescription(stateName string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if state, ok := sm.states[stateName]; ok {
		return state.Description
	}
	return ""
}

// AddStateChangeListener adds a listener for state changes
func (sm *StateManager) AddStateChangeListener(listener StateChangeListener) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.listeners = append(sm.listeners, listener)
}
