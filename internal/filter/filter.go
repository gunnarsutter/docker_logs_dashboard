package filter

import (
	"fmt"
	"regexp"
	"sync"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
)

// Filter processes log entries and filters them based on patterns
type Filter struct {
	name        string
	description string
	patterns    []*regexp.Regexp
	exclude     bool
	mu          sync.RWMutex
}

// Manager manages multiple filters
type Manager struct {
	filters map[string]*Filter
	mu      sync.RWMutex
}

// NewManager creates a new filter manager
func NewManager(filterConfigs []config.Filter) (*Manager, error) {
	m := &Manager{
		filters: make(map[string]*Filter),
	}

	for _, fc := range filterConfigs {
		filter, err := newFilter(fc)
		if err != nil {
			return nil, fmt.Errorf("failed to create filter '%s': %w", fc.Name, err)
		}
		m.filters[fc.Name] = filter
	}

	return m, nil
}

// newFilter creates a new filter from configuration
func newFilter(fc config.Filter) (*Filter, error) {
	f := &Filter{
		name:        fc.Name,
		description: fc.Description,
		patterns:    make([]*regexp.Regexp, 0, len(fc.Patterns)),
		exclude:     fc.Exclude,
	}

	for _, pattern := range fc.Patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern '%s': %w", pattern, err)
		}
		f.patterns = append(f.patterns, re)
	}

	return f, nil
}

// Matches checks if a log entry matches this filter
func (f *Filter) Matches(entry docker.LogEntry) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.patterns) == 0 {
		return true // No patterns means match all
	}

	for _, pattern := range f.patterns {
		if pattern.MatchString(entry.Message) {
			return true
		}
	}

	return false
}

// GetMatchingFilters returns all non-exclude filters that match the given log entry
func (m *Manager) GetMatchingFilters(entry docker.LogEntry) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matching []string
	for name, filter := range m.filters {
		if !filter.exclude && filter.Matches(entry) {
			matching = append(matching, name)
		}
	}

	return matching
}

// IsExcluded reports whether the entry matches any exclude filter.
func (m *Manager) IsExcluded(entry docker.LogEntry) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, filter := range m.filters {
		if filter.exclude && filter.Matches(entry) {
			return true
		}
	}
	return false
}

// GetFilter returns a specific filter by name
func (m *Manager) GetFilter(name string) (*Filter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter, ok := m.filters[name]
	return filter, ok
}

// AllFilters returns all filter names
func (m *Manager) AllFilters() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.filters))
	for name := range m.filters {
		names = append(names, name)
	}
	return names
}
