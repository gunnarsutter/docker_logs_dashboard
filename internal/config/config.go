package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Containers []Container `yaml:"containers"`
	States     []State     `yaml:"states"`
	Events     []Event     `yaml:"events"`
	Filters    []Filter    `yaml:"filters"`
}

// Container represents a Docker container to monitor
type Container struct {
	Name                string        `yaml:"name"`
	ContainerID         string        `yaml:"container_id"`
	StatusChecks        []StatusCheck `yaml:"status_checks"`
	RetainLogsOnRestart *bool         `yaml:"retain_logs_on_restart"`
	States              []State       `yaml:"states"`
	Events              []Event       `yaml:"events"`
	Filters             []Filter      `yaml:"filters"`
}

// StatusCheck defines a custom status indicator for a container driven by log pattern matching
type StatusCheck struct {
	Key           string               `yaml:"key"`
	Label         string               `yaml:"label"`
	InitialStatus string               `yaml:"initial_status"`
	InitialValue  string               `yaml:"initial_value"`
	Patterns      []StatusCheckPattern `yaml:"patterns"`
}

// StatusCheckPattern maps log text to a status update.
// When text is a list, all terms must appear in the message in the given order.
// When text is a single string, type controls how matching is done.
type StatusCheckPattern struct {
	Type       string   `yaml:"type"`        // "contains" (default), "starts_with", "ends_with", "regex"
	Text       TextList `yaml:"text"`        // single string or ordered list of strings
	IgnoreCase bool     `yaml:"ignore_case"` // default false
	Status     string   `yaml:"status"`
	Value      string   `yaml:"value"`
}

// TextList accepts either a single YAML string or a sequence of strings.
type TextList []string

func (t *TextList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.SequenceNode {
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*t = list
		return nil
	}
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	*t = TextList{s}
	return nil
}

// CompiledStatusCheckPattern is a ready-to-use matcher built from a StatusCheckPattern.
type CompiledStatusCheckPattern struct {
	match  func(string) bool
	Status string
	Value  string
}

// Matches reports whether msg satisfies this pattern.
func (c *CompiledStatusCheckPattern) Matches(msg string) bool {
	return c.match(msg)
}

// Compile builds a CompiledStatusCheckPattern ready for matching.
func (p StatusCheckPattern) Compile() CompiledStatusCheckPattern {
	if len(p.Text) == 0 {
		return CompiledStatusCheckPattern{
			match:  func(string) bool { return false },
			Status: p.Status,
			Value:  p.Value,
		}
	}

	ignoreCase := p.IgnoreCase
	normalise := func(s string) string {
		if ignoreCase {
			return strings.ToLower(s)
		}
		return s
	}

	var matchFn func(string) bool

	if len(p.Text) > 1 {
		// Multiple terms: all must be present in the message in order.
		terms := make([]string, len(p.Text))
		for i, t := range p.Text {
			terms[i] = normalise(t)
		}
		matchFn = func(msg string) bool {
			s := normalise(msg)
			pos := 0
			for _, term := range terms {
				idx := strings.Index(s[pos:], term)
				if idx == -1 {
					return false
				}
				pos += idx + len(term)
			}
			return true
		}
	} else {
		term := normalise(p.Text[0])
		switch p.Type {
		case "starts_with":
			matchFn = func(msg string) bool {
				return strings.HasPrefix(normalise(msg), term)
			}
		case "ends_with":
			matchFn = func(msg string) bool {
				return strings.HasSuffix(normalise(msg), term)
			}
		case "regex":
			pattern := p.Text[0]
			if ignoreCase {
				pattern = "(?i)" + pattern
			}
			re := regexp.MustCompile(pattern)
			matchFn = re.MatchString
		default: // "contains" or ""
			matchFn = func(msg string) bool {
				return strings.Contains(normalise(msg), term)
			}
		}
	}

	return CompiledStatusCheckPattern{
		match:  matchFn,
		Status: p.Status,
		Value:  p.Value,
	}
}

// State represents a system state
type State struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Event represents an event that can trigger state transitions
type Event struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
	State   string `yaml:"state"`
}

// Filter represents a log filter configuration
type Filter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Patterns    []string `yaml:"patterns"`
	Exclude     bool     `yaml:"exclude"` // if true, matching lines are suppressed from the log view
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if len(c.Containers) == 0 {
		return fmt.Errorf("no containers defined")
	}

	// Enforce unique container names — they are used as keys throughout the dashboard
	seenNames := make(map[string]bool, len(c.Containers))
	for _, container := range c.Containers {
		if seenNames[container.Name] {
			return fmt.Errorf("duplicate container name %q: each entry must have a unique name", container.Name)
		}
		seenNames[container.Name] = true
	}

	// Validate global states
	globalStateMap := make(map[string]bool)
	for _, state := range c.States {
		if state.Name == "" {
			return fmt.Errorf("state name cannot be empty")
		}
		globalStateMap[state.Name] = true
	}

	// Validate global events
	for _, event := range c.Events {
		if event.Name == "" {
			return fmt.Errorf("event name cannot be empty")
		}
		if event.Pattern == "" {
			return fmt.Errorf("event pattern cannot be empty")
		}
		if !globalStateMap[event.State] {
			return fmt.Errorf("event '%s' references undefined state '%s'", event.Name, event.State)
		}
	}

	for _, container := range c.Containers {
		// Validate per-container states/events when defined
		if len(container.States) > 0 || len(container.Events) > 0 {
			// Build the effective state map: per-container states take precedence over globals
			effectiveStateMap := globalStateMap
			if len(container.States) > 0 {
				effectiveStateMap = make(map[string]bool)
				for _, s := range container.States {
					if s.Name == "" {
						return fmt.Errorf("container '%s': state name cannot be empty", container.Name)
					}
					effectiveStateMap[s.Name] = true
				}
			}
			for _, event := range container.Events {
				if event.Name == "" {
					return fmt.Errorf("container '%s': event name cannot be empty", container.Name)
				}
				if event.Pattern == "" {
					return fmt.Errorf("container '%s': event pattern cannot be empty", container.Name)
				}
				if !effectiveStateMap[event.State] {
					return fmt.Errorf("container '%s': event '%s' references undefined state '%s'", container.Name, event.Name, event.State)
				}
			}
		}

		for _, sc := range container.StatusChecks {
			for _, p := range sc.Patterns {
				if len(p.Text) == 0 {
					return fmt.Errorf("container '%s' status check '%s' has a pattern with empty text", container.Name, sc.Key)
				}
				switch p.Type {
				case "", "contains", "starts_with", "ends_with":
					// valid
				case "regex":
					if len(p.Text) != 1 {
						return fmt.Errorf("container '%s' status check '%s': regex type requires a single text value", container.Name, sc.Key)
					}
					if _, err := regexp.Compile(p.Text[0]); err != nil {
						return fmt.Errorf("container '%s' status check '%s' has invalid regex '%s': %w", container.Name, sc.Key, p.Text[0], err)
					}
				default:
					return fmt.Errorf("container '%s' status check '%s' has unknown match type '%s'", container.Name, sc.Key, p.Type)
				}
			}
		}
	}

	return nil
}

// ValidateMinimal checks if the configuration has at least containers (for dynamic configs without states/events)
func (c *Config) ValidateMinimal() error {
	if len(c.Containers) == 0 {
		return fmt.Errorf("no containers defined")
	}
	return nil
}

// CreateMinimalConfig creates an empty configuration with no containers, states, or events
func CreateMinimalConfig() *Config {
	return &Config{
		Containers: []Container{},
		States:     []State{},
		Events:     []Event{},
		Filters:    []Filter{},
	}
}

// CreateConfigFromContainers creates a configuration from a list of container IDs and names
func CreateConfigFromContainers(containers []struct {
	ID   string
	Name string
}) *Config {
	cfg := CreateMinimalConfig()

	for _, c := range containers {
		containerName := c.Name
		if containerName == "" {
			containerName = c.ID[:12]
		}
		if containerName == "/" {
			containerName = c.ID[:12]
		} else if containerName[0] == '/' {
			containerName = containerName[1:]
		}

		cfg.Containers = append(cfg.Containers, Container{
			Name:         containerName,
			ContainerID:  containerName,
			StatusChecks: []StatusCheck{}, // No status checks for dynamic configs
		})
	}

	return cfg
}
