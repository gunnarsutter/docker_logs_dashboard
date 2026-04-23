package filter

import (
	"testing"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newManager(t *testing.T, filters []config.Filter) *Manager {
	t.Helper()
	m, err := NewManager(filters)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func logEntry(msg string) docker.LogEntry {
	return docker.LogEntry{ContainerName: "test", Message: msg}
}

// ── NewManager ────────────────────────────────────────────────────────────────

func TestNewManager_InvalidPattern(t *testing.T) {
	_, err := NewManager([]config.Filter{
		{Name: "bad", Patterns: []string{`[invalid`}},
	})
	if err == nil {
		t.Fatal("expected error for invalid regex pattern, got nil")
	}
}

func TestNewManager_Empty(t *testing.T) {
	m := newManager(t, nil)
	if len(m.AllFilters()) != 0 {
		t.Error("expected no filters")
	}
}

// ── GetMatchingFilters ────────────────────────────────────────────────────────

func TestGetMatchingFilters_Match(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
	})
	matches := m.GetMatchingFilters(logEntry("ERROR: timeout"))
	if len(matches) != 1 || matches[0] != "errors" {
		t.Errorf("expected [errors], got %v", matches)
	}
}

func TestGetMatchingFilters_NoMatch(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
	})
	matches := m.GetMatchingFilters(logEntry("INFO: all good"))
	if len(matches) != 0 {
		t.Errorf("expected no matches, got %v", matches)
	}
}

func TestGetMatchingFilters_MultipleFilters(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
		{Name: "warnings", Patterns: []string{`WARN`}},
		{Name: "both", Patterns: []string{`ERROR`, `WARN`}},
	})
	matches := m.GetMatchingFilters(logEntry("ERROR: something bad"))
	// "errors" and "both" should match; "warnings" should not
	matchSet := make(map[string]bool)
	for _, m := range matches {
		matchSet[m] = true
	}
	if !matchSet["errors"] {
		t.Error("expected 'errors' to match")
	}
	if !matchSet["both"] {
		t.Error("expected 'both' to match")
	}
	if matchSet["warnings"] {
		t.Error("expected 'warnings' not to match")
	}
}

func TestGetMatchingFilters_ExcludeFiltersNotReturned(t *testing.T) {
	// Exclude filters should not appear in GetMatchingFilters
	m := newManager(t, []config.Filter{
		{Name: "noise", Patterns: []string{`DEBUG`}, Exclude: true},
	})
	matches := m.GetMatchingFilters(logEntry("DEBUG: verbose output"))
	if len(matches) != 0 {
		t.Errorf("exclude filter should not appear in GetMatchingFilters, got %v", matches)
	}
}

func TestGetMatchingFilters_EmptyPatterns_MatchesAll(t *testing.T) {
	// A filter with no patterns matches everything
	m := newManager(t, []config.Filter{
		{Name: "catch-all", Patterns: nil},
	})
	if got := m.GetMatchingFilters(logEntry("anything")); len(got) != 1 {
		t.Errorf("expected catch-all filter to match, got %v", got)
	}
}

// ── IsExcluded ────────────────────────────────────────────────────────────────

func TestIsExcluded_ExcludeMatch(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "noise", Patterns: []string{`DEBUG`}, Exclude: true},
	})
	if !m.IsExcluded(logEntry("DEBUG: verbose")) {
		t.Error("expected entry to be excluded")
	}
}

func TestIsExcluded_ExcludeNoMatch(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "noise", Patterns: []string{`DEBUG`}, Exclude: true},
	})
	if m.IsExcluded(logEntry("INFO: startup complete")) {
		t.Error("expected entry not to be excluded")
	}
}

func TestIsExcluded_NoExcludeFilters(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
	})
	if m.IsExcluded(logEntry("ERROR: something")) {
		t.Error("non-exclude filter should not cause exclusion")
	}
}

func TestIsExcluded_MixedFilters(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
		{Name: "noise", Patterns: []string{`DEBUG`}, Exclude: true},
	})
	if m.IsExcluded(logEntry("ERROR: important")) {
		t.Error("should not be excluded just because it matches a non-exclude filter")
	}
	if !m.IsExcluded(logEntry("DEBUG: low level")) {
		t.Error("should be excluded because it matches an exclude filter")
	}
}

// ── GetFilter / AllFilters ────────────────────────────────────────────────────

func TestGetFilter_Exists(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "errors", Patterns: []string{`ERROR`}},
	})
	f, ok := m.GetFilter("errors")
	if !ok || f == nil {
		t.Fatal("expected to find filter 'errors'")
	}
}

func TestGetFilter_NotExists(t *testing.T) {
	m := newManager(t, nil)
	_, ok := m.GetFilter("nonexistent")
	if ok {
		t.Fatal("expected filter not found")
	}
}

func TestAllFilters(t *testing.T) {
	m := newManager(t, []config.Filter{
		{Name: "a", Patterns: []string{`A`}},
		{Name: "b", Patterns: []string{`B`}},
	})
	names := m.AllFilters()
	if len(names) != 2 {
		t.Errorf("expected 2 filters, got %d", len(names))
	}
}
