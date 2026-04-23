package config

import (
"os"
"path/filepath"
"testing"

"gopkg.in/yaml.v3"
)

// ── Validate ──────────────────────────────────────────────────────────────────

func TestValidate_NoContainers(t *testing.T) {
cfg := &Config{}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for empty containers, got nil")
}
}

func TestValidate_DuplicateContainerNames(t *testing.T) {
cfg := &Config{
Containers: []Container{
{Name: "app", ContainerID: "app"},
{Name: "app", ContainerID: "app2"},
},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for duplicate container name, got nil")
}
}

func TestValidate_EmptyStateName(t *testing.T) {
cfg := &Config{
Containers: []Container{{Name: "app", ContainerID: "app"}},
States:     []State{{Name: ""}},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for empty state name, got nil")
}
}

func TestValidate_EventReferencesUndefinedState(t *testing.T) {
cfg := &Config{
Containers: []Container{{Name: "app", ContainerID: "app"}},
States:     []State{{Name: "healthy"}},
Events:     []Event{{Name: "err", Pattern: "error", State: "nonexistent"}},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for event referencing undefined state, got nil")
}
}

func TestValidate_EmptyEventPattern(t *testing.T) {
cfg := &Config{
Containers: []Container{{Name: "app", ContainerID: "app"}},
States:     []State{{Name: "healthy"}},
Events:     []Event{{Name: "e", Pattern: "", State: "healthy"}},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for empty event pattern, got nil")
}
}

func TestValidate_ValidMinimal(t *testing.T) {
cfg := &Config{
Containers: []Container{{Name: "app", ContainerID: "app"}},
}
if err := cfg.Validate(); err != nil {
t.Fatalf("unexpected error for valid minimal config: %v", err)
}
}

func TestValidate_ValidWithStatesAndEvents(t *testing.T) {
cfg := &Config{
Containers: []Container{{Name: "app", ContainerID: "app"}},
States:     []State{{Name: "healthy"}, {Name: "degraded"}},
Events:     []Event{{Name: "error", Pattern: `ERROR`, State: "degraded"}},
}
if err := cfg.Validate(); err != nil {
t.Fatalf("unexpected error: %v", err)
}
}

func TestValidate_StatusCheck_InvalidRegex(t *testing.T) {
cfg := &Config{
Containers: []Container{
{
Name:        "app",
ContainerID: "app",
StatusChecks: []StatusCheck{
{
Key: "conn",
Patterns: []StatusCheckPattern{
{Type: "regex", Text: TextList{`[invalid`}, Status: "ok", Value: ""},
},
},
},
},
},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for invalid regex in status check, got nil")
}
}

func TestValidate_StatusCheck_UnknownType(t *testing.T) {
cfg := &Config{
Containers: []Container{
{
Name:        "app",
ContainerID: "app",
StatusChecks: []StatusCheck{
{
Key: "conn",
Patterns: []StatusCheckPattern{
{Type: "fuzzy", Text: TextList{"hello"}, Status: "ok", Value: ""},
},
},
},
},
},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for unknown match type, got nil")
}
}

func TestValidate_PerContainerStates_EventReferencesUndefined(t *testing.T) {
cfg := &Config{
Containers: []Container{
{
Name:        "app",
ContainerID: "app",
States:      []State{{Name: "running"}},
Events:      []Event{{Name: "err", Pattern: "ERR", State: "stopped"}},
},
},
}
if err := cfg.Validate(); err == nil {
t.Fatal("expected error for per-container event referencing undefined state, got nil")
}
}

func TestValidate_MultipleDifferentContainers(t *testing.T) {
cfg := &Config{
Containers: []Container{
{Name: "svc-a", ContainerID: "svc-a"},
{Name: "svc-b", ContainerID: "svc-b"},
},
}
if err := cfg.Validate(); err != nil {
t.Fatalf("unexpected error for two distinct containers: %v", err)
}
}

// ── StatusCheckPattern.Compile ────────────────────────────────────────────────

func TestCompile_Contains(t *testing.T) {
p := StatusCheckPattern{Type: "contains", Text: TextList{"ERROR"}, Status: "error", Value: "err"}.Compile()
if !p.Matches("some ERROR occurred") {
t.Error("expected match for substring")
}
if p.Matches("all good") {
t.Error("expected no match")
}
if p.Status != "error" || p.Value != "err" {
t.Errorf("unexpected status/value: %s / %s", p.Status, p.Value)
}
}

func TestCompile_Contains_Default(t *testing.T) {
p := StatusCheckPattern{Text: TextList{"warn"}, Status: "warn", Value: ""}.Compile()
if !p.Matches("low disk warn") {
t.Error("expected default contains match")
}
}

func TestCompile_Contains_IgnoreCase(t *testing.T) {
p := StatusCheckPattern{Type: "contains", Text: TextList{"error"}, IgnoreCase: true, Status: "error", Value: ""}.Compile()
if !p.Matches("some ERROR occurred") {
t.Error("expected case-insensitive match")
}
if !p.Matches("Error: timeout") {
t.Error("expected mixed-case match")
}
}

func TestCompile_StartsWith(t *testing.T) {
p := StatusCheckPattern{Type: "starts_with", Text: TextList{"WARN"}, Status: "warn", Value: ""}.Compile()
if !p.Matches("WARN: disk full") {
t.Error("expected match for starts_with")
}
if p.Matches("some WARN in middle") {
t.Error("expected no match when prefix doesn't match")
}
}

func TestCompile_StartsWith_IgnoreCase(t *testing.T) {
p := StatusCheckPattern{Type: "starts_with", Text: TextList{"warn"}, IgnoreCase: true, Status: "warn", Value: ""}.Compile()
if !p.Matches("WARN: disk full") {
t.Error("expected case-insensitive starts_with match")
}
}

func TestCompile_EndsWith(t *testing.T) {
p := StatusCheckPattern{Type: "ends_with", Text: TextList{"done"}, Status: "ok", Value: ""}.Compile()
if !p.Matches("task done") {
t.Error("expected match for ends_with")
}
if p.Matches("done task") {
t.Error("expected no match when suffix doesn't match")
}
}

func TestCompile_Regex(t *testing.T) {
p := StatusCheckPattern{Type: "regex", Text: TextList{`\d{3}`}, Status: "ok", Value: ""}.Compile()
if !p.Matches("code 200 ok") {
t.Error("expected regex match")
}
if p.Matches("no digits here") {
t.Error("expected no regex match")
}
}

func TestCompile_Regex_IgnoreCase(t *testing.T) {
p := StatusCheckPattern{Type: "regex", Text: TextList{`error`}, IgnoreCase: true, Status: "error", Value: ""}.Compile()
if !p.Matches("ERROR: something bad") {
t.Error("expected case-insensitive regex match")
}
}

func TestCompile_MultiTerm_Ordered(t *testing.T) {
p := StatusCheckPattern{Text: TextList{"connected", "ready"}, Status: "ok", Value: ""}.Compile()
if !p.Matches("connected to broker, ready to receive") {
t.Error("expected ordered multi-term match")
}
if p.Matches("ready to receive, then connected") {
t.Error("expected no match when terms are out of order")
}
if p.Matches("connected to broker") {
t.Error("expected no match when second term is absent")
}
}

func TestCompile_MultiTerm_IgnoreCase(t *testing.T) {
p := StatusCheckPattern{Text: TextList{"CONN", "READY"}, IgnoreCase: true, Status: "ok", Value: ""}.Compile()
if !p.Matches("conn established, ready") {
t.Error("expected case-insensitive multi-term match")
}
}

func TestCompile_EmptyText_NeverMatches(t *testing.T) {
p := StatusCheckPattern{Text: TextList{}, Status: "ok", Value: ""}.Compile()
if p.Matches("anything") {
t.Error("empty text pattern should never match")
}
}

// ── TextList.UnmarshalYAML ────────────────────────────────────────────────────

func TestTextList_UnmarshalYAML_SingleString(t *testing.T) {
type wrapper struct {
Text TextList `yaml:"text"`
}
var w wrapper
if err := yaml.Unmarshal([]byte(`text: hello`), &w); err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(w.Text) != 1 || w.Text[0] != "hello" {
t.Errorf("expected [hello], got %v", w.Text)
}
}

func TestTextList_UnmarshalYAML_Sequence(t *testing.T) {
type wrapper struct {
Text TextList `yaml:"text"`
}
var w wrapper
if err := yaml.Unmarshal([]byte("text:\n  - foo\n  - bar"), &w); err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(w.Text) != 2 || w.Text[0] != "foo" || w.Text[1] != "bar" {
t.Errorf("expected [foo bar], got %v", w.Text)
}
}

// ── Load ──────────────────────────────────────────────────────────────────────

func TestLoad_FileNotFound(t *testing.T) {
if _, err := Load("/nonexistent/path/config.yaml"); err == nil {
t.Fatal("expected error for missing file, got nil")
}
}

func TestLoad_InvalidYAML(t *testing.T) {
f := writeTempFile(t, "not: [valid yaml")
if _, err := Load(f); err == nil {
t.Fatal("expected error for invalid YAML, got nil")
}
}

func TestLoad_ValidFile(t *testing.T) {
content := `
containers:
  - name: myapp
    container_id: myapp
states:
  - name: healthy
events:
  - name: err
    pattern: "ERROR"
    state: healthy
`
f := writeTempFile(t, content)
cfg, err := Load(f)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(cfg.Containers) != 1 || cfg.Containers[0].Name != "myapp" {
t.Errorf("unexpected containers: %v", cfg.Containers)
}
if len(cfg.States) != 1 || cfg.States[0].Name != "healthy" {
t.Errorf("unexpected states: %v", cfg.States)
}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeTempFile(t *testing.T, content string) string {
t.Helper()
dir := t.TempDir()
path := filepath.Join(dir, "config.yaml")
if err := os.WriteFile(path, []byte(content), 0644); err != nil {
t.Fatalf("failed to write temp file: %v", err)
}
return path
}
