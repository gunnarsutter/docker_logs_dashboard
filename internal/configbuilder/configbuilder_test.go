package configbuilder

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── extractMessage ────────────────────────────────────────────────────────────

func TestExtractMessage_WithTimestamp(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"12:34:56 hello world", "hello world"},
		{"00:00:00 startup", "startup"},
		{"23:59:59 shutdown complete", "shutdown complete"},
	}
	for _, c := range cases {
		if got := extractMessage(c.input); got != c.want {
			t.Errorf("extractMessage(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractMessage_NoTimestamp(t *testing.T) {
	input := "no timestamp here"
	if got := extractMessage(input); got != input {
		t.Errorf("extractMessage(%q) = %q, want unchanged", input, got)
	}
}

func TestExtractMessage_ShortLine(t *testing.T) {
	input := "hi"
	if got := extractMessage(input); got != input {
		t.Errorf("expected unchanged short line, got %q", got)
	}
}

// ── sanitizeName ─────────────────────────────────────────────────────────────

func TestSanitizeName_Normal(t *testing.T) {
	if got := sanitizeName("my-config"); got != "my-config" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestSanitizeName_StripYamlSuffix(t *testing.T) {
	if got := sanitizeName("my-config.yaml"); got != "my-config" {
		t.Errorf("expected .yaml stripped, got %q", got)
	}
}

func TestSanitizeName_PathTraversal(t *testing.T) {
	got := sanitizeName("../../etc/passwd")
	if strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Errorf("path traversal not stripped: %q", got)
	}
}

func TestSanitizeName_SpecialChars(t *testing.T) {
	got := sanitizeName("hello world!")
	if strings.Contains(got, " ") || strings.Contains(got, "!") {
		t.Errorf("special chars not sanitized: %q", got)
	}
}

func TestSanitizeName_Empty(t *testing.T) {
	if got := sanitizeName(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSanitizeName_DotOnly(t *testing.T) {
	if got := sanitizeName("."); got != "" {
		t.Errorf("expected empty for '.', got %q", got)
	}
}

// ── suggestPattern ────────────────────────────────────────────────────────────

func TestSuggestPattern_DigitsReplaced(t *testing.T) {
	got := suggestPattern("connected 42 nodes in 100ms")
	if strings.Contains(got, "42") || strings.Contains(got, "100") {
		t.Errorf("expected digits replaced with \\d+, got %q", got)
	}
	if !strings.Contains(got, `\d+`) {
		t.Errorf("expected \\d+ in pattern, got %q", got)
	}
}

func TestSuggestPattern_LongMessageTruncated(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := suggestPattern(long)
	// After quoting the truncated version (100 chars of 'a'), should be <= 100 a's
	if strings.Count(got, "a") > 100 {
		t.Errorf("expected message truncated to 100 chars before pattern generation")
	}
}

func TestSuggestPattern_SpecialCharsEscaped(t *testing.T) {
	got := suggestPattern("error [main.go:42]")
	if strings.Contains(got, "[") && !strings.Contains(got, `\[`) {
		t.Errorf("expected [ to be escaped in pattern, got %q", got)
	}
}

// ── BuildConfigYAML ───────────────────────────────────────────────────────────

func TestBuildConfigYAML_NoDrafts(t *testing.T) {
	_, err := BuildConfigYAML(nil)
	if err == nil {
		t.Fatal("expected error for empty drafts")
	}
}

func TestBuildConfigYAML_SingleEvent(t *testing.T) {
	drafts := []EventDraft{
		{EventName: "startup", Pattern: `started`, State: "running"},
	}
	yaml, err := BuildConfigYAML(drafts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(yaml, "startup") {
		t.Error("expected event name in output")
	}
	if !strings.Contains(yaml, "started") {
		t.Error("expected pattern in output")
	}
}

func TestBuildConfigYAML_WithFilter(t *testing.T) {
	drafts := []EventDraft{
		{EventName: "err", Pattern: `ERROR`, State: "error", AddFilter: true, FilterName: "errors"},
	}
	yaml, err := BuildConfigYAML(drafts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(yaml, "errors") {
		t.Error("expected filter name in output")
	}
	if !strings.Contains(yaml, "filters:") {
		t.Error("expected filters section in output")
	}
}

func TestBuildConfigYAML_SharedFilter_MultiplePatterns(t *testing.T) {
	drafts := []EventDraft{
		{EventName: "err1", Pattern: `ERROR`, State: "error", AddFilter: true, FilterName: "important"},
		{EventName: "err2", Pattern: `CRITICAL`, State: "error", AddFilter: true, FilterName: "important"},
	}
	yaml, err := BuildConfigYAML(drafts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "important" filter should appear once but contain both patterns
	count := strings.Count(yaml, "important")
	if count != 1 {
		t.Errorf("expected filter name to appear once, got %d", count)
	}
	if !strings.Contains(yaml, "ERROR") || !strings.Contains(yaml, "CRITICAL") {
		t.Error("expected both patterns in filter")
	}
}

// ── ParseConfigDrafts round-trip ──────────────────────────────────────────────

func TestParseConfigDrafts_RoundTrip(t *testing.T) {
	original := []EventDraft{
		{EventName: "startup", Pattern: `starting up`, State: "running"},
		{EventName: "error", Pattern: `ERROR`, State: "degraded", AddFilter: true, FilterName: "errors"},
	}
	yaml, err := BuildConfigYAML(original)
	if err != nil {
		t.Fatalf("BuildConfigYAML: %v", err)
	}

	recovered, err := ParseConfigDrafts([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseConfigDrafts: %v", err)
	}

	// Build maps for order-independent comparison
	origMap := make(map[string]EventDraft)
	for _, d := range original {
		origMap[d.EventName] = d
	}
	for _, d := range recovered {
		if orig, ok := origMap[d.EventName]; ok {
			if d.Pattern != orig.Pattern {
				t.Errorf("event %q: pattern mismatch: got %q want %q", d.EventName, d.Pattern, orig.Pattern)
			}
			if d.AddFilter != orig.AddFilter {
				t.Errorf("event %q: AddFilter mismatch: got %v want %v", d.EventName, d.AddFilter, orig.AddFilter)
			}
			if d.FilterName != orig.FilterName {
				t.Errorf("event %q: FilterName mismatch: got %q want %q", d.EventName, d.FilterName, orig.FilterName)
			}
		}
	}
}

func TestParseConfigDrafts_InvalidYAML(t *testing.T) {
	_, err := ParseConfigDrafts([]byte("not: [valid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// ── ParseLogFile ──────────────────────────────────────────────────────────────

func TestParseLogFile_Basic(t *testing.T) {
	content := "12:34:56 connected to broker\n12:34:57 ready\n\n"
	f := writeTempLog(t, content)
	lines, err := ParseLogFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].Message != "connected to broker" {
		t.Errorf("unexpected message: %q", lines[0].Message)
	}
	if lines[1].Raw != "12:34:57 ready" {
		t.Errorf("unexpected raw: %q", lines[1].Raw)
	}
}

func TestParseLogFile_EmptyLinesSkipped(t *testing.T) {
	content := "\n   \nactual line\n\n"
	f := writeTempLog(t, content)
	lines, err := ParseLogFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d", len(lines))
	}
}

func TestParseLogFile_NotFound(t *testing.T) {
	_, err := ParseLogFile("/nonexistent/file.log")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ── API handlers (via httptest) ───────────────────────────────────────────────

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	configsDir := filepath.Join(dir, "configs")
	logsDir := filepath.Join(dir, "logs")
	srv, err := NewServer("", logsDir, "out.yaml", "", configsDir, "0")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, dir
}

func TestHandleConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp sessionConfig
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OutputFile != "out.yaml" {
		t.Errorf("unexpected output_file: %q", resp.OutputFile)
	}
}

func TestHandleStates_DefaultStates(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleStates(rec, httptest.NewRequest(http.MethodGet, "/api/states", nil))

	var states []string
	if err := json.NewDecoder(rec.Body).Decode(&states); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(states) == 0 {
		t.Error("expected default states to be returned")
	}
}

func TestHandleLines_Empty(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleLines(rec, httptest.NewRequest(http.MethodGet, "/api/lines", nil))

	var lines []lineResponse
	if err := json.NewDecoder(rec.Body).Decode(&lines); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected empty lines, got %d", len(lines))
	}
}

func TestHandleGenerate_Valid(t *testing.T) {
	srv, _ := newTestServer(t)
	body := map[string]any{
		"drafts": []map[string]any{
			{"event_name": "startup", "pattern": `started`, "state": "running"},
		},
	}
	rec := postJSON(t, srv.handleGenerate, "/api/generate", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["yaml"] == "" {
		t.Error("expected non-empty yaml in response")
	}
}

func TestHandleGenerate_MissingEventName(t *testing.T) {
	srv, _ := newTestServer(t)
	body := map[string]any{
		"drafts": []map[string]any{
			{"event_name": "", "pattern": `started`, "state": "running"},
		},
	}
	rec := postJSON(t, srv.handleGenerate, "/api/generate", body)

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["error"] == "" {
		t.Error("expected error for missing event name")
	}
}

func TestHandleGenerate_InvalidRegex(t *testing.T) {
	srv, _ := newTestServer(t)
	body := map[string]any{
		"drafts": []map[string]any{
			{"event_name": "e", "pattern": `[bad`, "state": "running"},
		},
	}
	rec := postJSON(t, srv.handleGenerate, "/api/generate", body)

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["error"] == "" {
		t.Error("expected error for invalid regex")
	}
}

func TestHandleGenerate_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleGenerate(rec, httptest.NewRequest(http.MethodGet, "/api/generate", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleSavedConfigs_ListAndCRUD(t *testing.T) {
	srv, _ := newTestServer(t)

	// List empty
	rec := httptest.NewRecorder()
	srv.handleSavedConfigs(rec, httptest.NewRequest(http.MethodGet, "/api/saved-configs", nil))
	var list []SavedConfigMeta
	json.NewDecoder(rec.Body).Decode(&list) //nolint:errcheck
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}

	// PUT a config
	putBody := map[string]string{"content": "containers:\n  - name: test\n"}
	putRec := putJSON(t, srv.handleSavedConfigItem, "/api/saved-configs/myconfig", putBody)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT failed: %d %s", putRec.Code, putRec.Body.String())
	}

	// GET it back
	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/saved-configs/myconfig", nil)
	srv.handleSavedConfigItem(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET failed: %d", getRec.Code)
	}
	var getResp map[string]string
	json.NewDecoder(getRec.Body).Decode(&getResp) //nolint:errcheck
	if getResp["name"] != "myconfig" {
		t.Errorf("unexpected name: %q", getResp["name"])
	}

	// List again — should have one entry
	listRec := httptest.NewRecorder()
	srv.handleSavedConfigs(listRec, httptest.NewRequest(http.MethodGet, "/api/saved-configs", nil))
	json.NewDecoder(listRec.Body).Decode(&list) //nolint:errcheck
	if len(list) != 1 {
		t.Errorf("expected 1 saved config, got %d", len(list))
	}

	// DELETE it
	delRec := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, "/api/saved-configs/myconfig", nil)
	srv.handleSavedConfigItem(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE failed: %d", delRec.Code)
	}

	// Should be gone
	getRec2 := httptest.NewRecorder()
	srv.handleSavedConfigItem(getRec2, httptest.NewRequest(http.MethodGet, "/api/saved-configs/myconfig", nil))
	if getRec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getRec2.Code)
	}
}

func TestHandleSavedConfigItem_PathTraversalRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/saved-configs/../../etc/passwd", nil)
	srv.handleSavedConfigItem(rec, req)
	// sanitizeName strips the traversal; file won't exist → 404, not a real passwd read
	if rec.Code == http.StatusOK {
		t.Error("path traversal should not succeed")
	}
}

func TestHandleSavedConfigItem_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/saved-configs/", nil)
	srv.handleSavedConfigItem(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", rec.Code)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeTempLog(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.log")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content) //nolint:errcheck
	f.Close()
	return f.Name()
}

func postJSON(t *testing.T, handler http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	handler(rec, req)
	return rec
}

func putJSON(t *testing.T, handler http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, io.NopCloser(bytes.NewReader(data)))
	req.Header.Set("Content-Type", "application/json")
	handler(rec, req)
	return rec
}
