package configbuilder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"docker-logs-dashboard/internal/config"
)

var defaultStates = []string{
	"initializing",
	"connecting",
	"operational",
	"degraded",
	"critical",
}

// EventDraft holds a user-configured event built from a selected log line.
type EventDraft struct {
	EventName  string `json:"event_name"`
	Pattern    string `json:"pattern"`
	State      string `json:"state"`
	AddFilter  bool   `json:"add_filter"`
	FilterName string `json:"filter_name"`
}

type lineResponse struct {
	Index            int    `json:"index"`
	Raw              string `json:"raw"`
	Message          string `json:"message"`
	SuggestedPattern string `json:"suggested_pattern"`
}

type sessionConfig struct {
	OutputFile string `json:"output_file"`
	ConfigsDir string `json:"configs_dir"`
	LogsDir    string `json:"logs_dir"`
}

// SavedConfigMeta is returned in the list endpoint.
type SavedConfigMeta struct {
	Name     string `json:"name"`
	Modified string `json:"modified"`
	Size     int64  `json:"size"`
}

// Server is the config-builder HTTP server.
type Server struct {
	lines      []LogLine
	states     []string
	outputFile string
	configsDir string
	logsDir    string
	addr       string
}

// SavedLogMeta is returned in the saved-logs list endpoint.
type SavedLogMeta struct {
	Name     string `json:"name"`
	Modified string `json:"modified"`
	Size     int64  `json:"size"`
}

// NewServer creates a new Server instance.
func NewServer(logFile, logsDir, outputFile, existingConfig, configsDir, port string) (*Server, error) {
	var lines []LogLine
	if logFile != "" {
		var err error
		lines, err = ParseLogFile(logFile)
		if err != nil {
			return nil, err
		}
	}

	states := make([]string, len(defaultStates))
	copy(states, defaultStates)

	if existingConfig != "" {
		cfg, cfgErr := config.Load(existingConfig)
		if cfgErr == nil && len(cfg.States) > 0 {
			states = make([]string, len(cfg.States))
			for i, s := range cfg.States {
				states[i] = s.Name
			}
		}
	}

	if err := os.MkdirAll(configsDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create configs directory %q: %w", configsDir, err)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create logs directory %q: %w", logsDir, err)
	}

	return &Server{
		lines:      lines,
		states:     states,
		outputFile: outputFile,
		configsDir: configsDir,
		logsDir:    logsDir,
		addr:       "127.0.0.1:" + port,
	}, nil
}

// Start registers routes and starts the HTTP server (blocking).
func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/lines", s.handleLines)
	mux.HandleFunc("/api/states", s.handleStates)
	mux.HandleFunc("/api/generate", s.handleGenerate)
	mux.HandleFunc("/api/saved-configs", s.handleSavedConfigs)
	mux.HandleFunc("/api/saved-configs/", s.handleSavedConfigItem)
	mux.HandleFunc("/api/saved-logs", s.handleSavedLogs)
	mux.HandleFunc("/api/saved-logs/", s.handleSavedLogLines)

	return http.ListenAndServe(s.addr, mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, sessionConfig{OutputFile: s.outputFile, ConfigsDir: s.configsDir, LogsDir: s.logsDir})
}

func (s *Server) handleLines(w http.ResponseWriter, _ *http.Request) {
	resp := make([]lineResponse, len(s.lines))
	for i, l := range s.lines {
		resp[i] = lineResponse{
			Index:            i,
			Raw:              l.Raw,
			Message:          l.Message,
			SuggestedPattern: suggestPattern(l.Message),
		}
	}
	writeJSON(w, resp)
}

func (s *Server) handleStates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.states)
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Name   string       `json:"name"`
		Drafts []EventDraft `json:"drafts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	for i, d := range body.Drafts {
		if strings.TrimSpace(d.EventName) == "" {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("event %d: name is required", i+1)})
			return
		}
		if strings.TrimSpace(d.Pattern) == "" {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("event %d: pattern is required", i+1)})
			return
		}
		if _, err := regexp.Compile(d.Pattern); err != nil {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("event %d: invalid regex: %v", i+1, err)})
			return
		}
		if d.AddFilter && strings.TrimSpace(d.FilterName) == "" {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("event %d: filter name required when 'add as filter' is enabled", i+1)})
			return
		}
	}

	yamlStr, err := BuildConfigYAML(body.Drafts)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	// Save to configs dir if a name was provided
	savedAs := ""
	if name := sanitizeName(body.Name); name != "" {
		savedAs, err = s.saveConfig(name, yamlStr)
		if err != nil {
			writeJSON(w, map[string]string{"error": "saved YAML but could not write file: " + err.Error()})
			return
		}
	}

	writeJSON(w, map[string]string{"yaml": yamlStr, "saved_as": savedAs})
}

// handleSavedConfigs handles GET (list) for /api/saved-configs
func (s *Server) handleSavedConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(s.configsDir)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	var list []SavedConfigMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		info, _ := e.Info()
		list = append(list, SavedConfigMeta{
			Name:     strings.TrimSuffix(e.Name(), ".yaml"),
			Modified: info.ModTime().Format(time.RFC3339),
			Size:     info.Size(),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	writeJSON(w, list)
}

// handleSavedConfigItem handles GET, PUT, DELETE for /api/saved-configs/{name}
func (s *Server) handleSavedConfigItem(w http.ResponseWriter, r *http.Request) {
	name := sanitizeName(strings.TrimPrefix(r.URL.Path, "/api/saved-configs/"))
	if name == "" {
		http.Error(w, "missing config name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.configsDir, name+".yaml")

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		if r.URL.Query().Get("as") == "drafts" {
			drafts, parseErr := ParseConfigDrafts(data)
			if parseErr != nil {
				writeJSON(w, map[string]string{"error": parseErr.Error()})
				return
			}
			writeJSON(w, map[string]any{"name": name, "drafts": drafts})
			return
		}
		writeJSON(w, map[string]string{"name": name, "content": string(data)})

	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(path, []byte(body.Content), 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"saved_as": name})

	case http.MethodDelete:
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		writeJSON(w, map[string]bool{"deleted": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// saveConfig writes yamlStr to configsDir/<name>.yaml and returns the filename.
func (s *Server) saveConfig(name, yamlStr string) (string, error) {
	path := filepath.Join(s.configsDir, name+".yaml")
	if err := os.WriteFile(path, []byte(yamlStr), 0644); err != nil {
		return "", err
	}
	return name, nil
}

// sanitizeName strips path traversal characters, spaces, and .yaml suffix.
func sanitizeName(name string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".yaml")
	name = filepath.Base(name) // strip any directory component
	// Keep only safe characters
	safe := regexp.MustCompile(`[^a-zA-Z0-9_\-.]`)
	name = safe.ReplaceAllString(name, "_")
	if name == "" || name == "." {
		return ""
	}
	return name
}

var digitRun = regexp.MustCompile(`\d+`)

// suggestPattern turns a plain log message into a reasonable regex pattern.
func suggestPattern(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 100 {
		msg = msg[:100]
	}
	escaped := regexp.QuoteMeta(msg)
	escaped = digitRun.ReplaceAllString(escaped, `\d+`)
	return escaped
}

// ── Saved-logs endpoints ──────────────────────────────────────────────────

// handleSavedLogs lists .log files in logsDir.
func (s *Server) handleSavedLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	entries, err := os.ReadDir(s.logsDir)
	if err != nil {
		writeJSON(w, []SavedLogMeta{})
		return
	}
	var list []SavedLogMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, _ := e.Info()
		list = append(list, SavedLogMeta{
			Name:     e.Name(),
			Modified: info.ModTime().Format(time.RFC3339),
			Size:     info.Size(),
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Modified > list[j].Modified })
	writeJSON(w, list)
}

// handleSavedLogLines returns parsed lines for a specific log file so the
// builder can load it without restarting the server.
func (s *Server) handleSavedLogLines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/api/saved-logs/"))
	if !strings.HasSuffix(name, ".log") {
		http.Error(w, "invalid log name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(s.logsDir, name)
	lines, err := ParseLogFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	resp := make([]lineResponse, len(lines))
	for i, l := range lines {
		resp[i] = lineResponse{
			Index:            i,
			Raw:              l.Raw,
			Message:          l.Message,
			SuggestedPattern: suggestPattern(l.Message),
		}
	}
	writeJSON(w, resp)
}

// ── Config ↔ drafts round-trip ────────────────────────────────────────────

// handleSavedConfigItem adds a GET ?as=drafts query that returns EventDraft
// slice instead of raw YAML, used by the form editor.
