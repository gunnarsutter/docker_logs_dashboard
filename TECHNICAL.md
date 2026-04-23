# Technical Description — Docker Logs Dashboard

## Overview

`docker-logs-dashboard` is a Go application with three entry points:

| Binary | Source | Purpose |
|---|---|---|
| `docker-logs-dashboard` | `main.go` | Full-screen TUI for real-time container log monitoring |
| `config-builder` | `cmd/config-builder/main.go` | Standalone HTTP API server for building config files from exported logs |
| `config-validator` | `cmd/config-validator/main.go` | Standalone CLI for validating config files and printing a summary |

The TUI binary can optionally start the config-builder API server in-process via the `-api` flag. The standalone `config-builder` binary exposes the same API without starting the dashboard.

---

## Module Graph

```
main.go
 ├── internal/config         — YAML config loader & validator
 ├── internal/docker         — Docker API client wrapper
 ├── internal/dashboard      — Orchestration (lifecycle coordinator)
 │    ├── internal/state     — Per-container state machine
 │    ├── internal/filter    — Per-container log filter engine
 │    ├── internal/status    — Running-status & custom-status tracker
 │    └── internal/ui        — Terminal UI (tview)
 ├── internal/ui             — ContainerSelector (pre-dashboard TUI)
 └── internal/configbuilder  — HTTP config-builder server
      ├── server.go          — HTTP handlers & route setup
      ├── logparse.go        — Log file parser
      └── writer.go          — YAML generation & round-trip parsing

cmd/config-builder/main.go
 └── internal/configbuilder  — (same package, standalone entry point)

cmd/config-validator/main.go
 └── internal/config         — config loader and validator entry point
```

---

## External Dependencies

| Library | Used by | Purpose |
|---|---|---|
| `github.com/docker/docker` | `internal/docker` | Docker Engine API client (container list, inspect, log streaming) |
| `github.com/gdamore/tcell/v2` | `internal/ui` | Low-level terminal cell/event handling (keyboard, colours) |
| `github.com/rivo/tview` | `internal/ui` | High-level TUI widget library built on tcell |
| `gopkg.in/yaml.v3` | `internal/config`, `internal/configbuilder` | YAML parsing and serialisation |
| Go standard library `net/http` | `internal/configbuilder` | HTTP server for the config-builder API |
| Go standard library `regexp` | `internal/state`, `internal/filter`, `internal/config`, `internal/configbuilder` | Regex pattern compilation and matching |

---

## External Resources

| Resource | Accessed by | How |
|---|---|---|
| Docker daemon socket (`/var/run/docker.sock` or `$DOCKER_HOST`) | `internal/docker` | `docker/docker` SDK; `client.FromEnv` reads `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` |
| Filesystem — config files (`configs/*.yaml`) | `main.go` → `internal/config` | `os.ReadFile` |
| Filesystem — exported log files (`~/logs/*.log`) | `internal/ui` (write), `internal/configbuilder` (read) | `os.Create` / `os.Open` |
| Filesystem — saved config files (`configs/*.yaml`) | `internal/configbuilder` | `os.WriteFile`, `os.ReadFile`, `os.Remove` |
| Filesystem — debug log files (`dashboard.log`, `docker-client.log`) | `internal/dashboard`, `internal/docker` | Append-only `os.OpenFile`; written to the working directory |

---

## Data Flow

### 1. Startup

```
main.go
  │
  ├─ docker.NewClient()             connects to Docker socket
  │
  ├─ config.Load(path)              reads YAML from disk, validates
  │    └─ if fails / -no-config ──► ui.NewContainerSelector (TUI)
  │                                   user picks containers
  │                                   config.CreateConfigFromContainers()
  │
  ├─ dashboard.New(cfg, docker)     compiles StatusCheckPatterns from config
  │
  ├─ [optional] configbuilder.NewServer().Start()  API-only HTTP server goroutine (no web UI)
  │             srv.ServeUI = false
  │
  └─ dash.Start(ctx)             ◄── blocks until UI exits
```

### 2. Dashboard Initialisation (`dashboard.Start`)

```
For each Container in config:
  ├─ state.NewStateManager(states, events)                          compiles event regexps
  │    resolution: container.States → config.States (fallback)
  ├─ filter.NewManager(filters)                                     compiles filter regexps
  │    resolution: container.Filters → config.Filters (fallback)
  └─ status.Tracker.RegisterService()
       status.Tracker.UpdateCustomStatus()                          sets initial values

ui.New(statusTracker, stateManagers, filterManagers, containers)
  └─ tview application created, one log page per container

goroutine: dashboard.processLogs(ctx)
goroutine: dashboard.monitorContainerStatus(ctx)
ui.Run()   ◄── blocks (tview event loop)
```

### 3. Log Streaming Pipeline

```
Docker daemon
  │  (HTTP chunked stream, Docker multiplexed log format)
  │
internal/docker.Client.StreamLogs()
  │  strips 8-byte Docker frame header per log line
  │  sends docker.LogEntry{ContainerName, ContainerID, Message} to:
  │
  ▼
per-container localChan  (buffered, capacity 100)
  │
forwarding goroutine (one per streaming container)
  │  sets entry.ContainerName = config.Container.Name
  │  (this is what enables multiple config entries to monitor
  │   the same Docker container independently)
  │
  ▼
dashboard.logChan  (shared buffered channel, capacity 100)
  │
  ▼
dashboard.processLogs()  [goroutine]
  │
  │  entry.ContainerName is already set to the config entry Name
  │  (no mapping needed)
  │
  ├─ stateManagers[containerName].ProcessLogEntry(entry)
  │    iterates compiled event patterns in order
  │    first match → updates currentState, fires StateChangeListeners (goroutines)
  │
  ├─ compiledStatusChecks[containerName]  (pre-compiled at New() time)
  │    iterates StatusCheck patterns (contains / starts_with / ends_with / regex)
  │    first match per check → status.Tracker.UpdateCustomStatus()
  │
  ├─ filterManagers[containerName].IsExcluded(entry)
  │    iterates exclude-filters; if any pattern matches → entry is dropped (continue)
  │
  ├─ filterManagers[containerName].GetMatchingFilters(entry)
  │    iterates non-exclude filters; collects names of those whose patterns match
  │
  └─ ui.AddLog(entry, matchingFilters)
       appends formatted line to ContainerLogView.buffer (max 500 lines)
       appends to ContainerLogView.pendingLines for next UI flush
```

### 4. Container Health Monitoring

```
dashboard.monitorContainerStatus()  [goroutine, ticks every 100 milliseconds]
  │
  └─ for each Container:
       docker.IsContainerRunningByName()
         └─ docker.ListContainers() → find by name → docker.ContainerInspect()
       │
       ├─ running & not streaming  ──► dashboard.startLogStreaming()
       │                                   goroutine: resolves current ID by name
       │                                   docker.StreamLogs() → logChan
       │
       ├─ running transition (stopped→running)
       │    ui.ClearContainerLog()  (if retain_logs_on_restart = false)
       │    ui.AddStatusMessageToContainer("Container is back up...")
       │
       └─ stopped transition (running→stopped)
            status.Tracker.UpdateServiceRunning(false)
            ui.AddStatusMessageToContainer("Container is not running")
```

### 5. UI Render Loop

```
ui.updateLoop()  [goroutine, ticks every 500 ms]
  │
  └─ app.QueueUpdateDraw()  ◄── all widget writes must happen here (tview constraint)
       │
       ├─ updateStatusBar()
       │    reads status.Tracker.GetAllServices()
       │    formats ● running/stopped indicator + custom status items per container
       │
       ├─ updateStateView()
       │    reads stateManagers[currentTab].GetCurrentState()
       │    reads stateManagers[currentTab].GetStateDescription()
       │    displays state description with colour coding
       │
       ├─ updateFilterView()
       │    reads filterManagers[currentTab].AllFilters()
       │    lists up to 4 filter names
       │
       ├─ updateTabBar()
       │    renders tab strip; active tab highlighted in aqua
       │
       └─ flushPendingLogs()
            for each ContainerLogView:
              pendingNeedsRedraw=true → full buffer rewrite (after trim)
              otherwise            → append pendingLines only
```

### 6. Log Export

```
user presses 'e'
  └─ ui.exportCurrentLog()
       copies ContainerLogView.buffer (locked)
       strips tview colour tags via regexp
       writes to ~/logs/<container>.<datetime>.log
```

---

## Package Responsibilities

### `internal/config`

- Defines all config data structures: `Config`, `Container`, `State`, `Event`, `Filter`, `StatusCheck`, `StatusCheckPattern`
- `TextList` — custom YAML unmarshaler accepting scalar or sequence
- `StatusCheckPattern.Compile()` — builds a `CompiledStatusCheckPattern` with a closure-based `match func(string) bool`; supports `contains`, `starts_with`, `ends_with`, `regex`, and ordered multi-term matching
- `Load(path)` — reads YAML, unmarshals, calls `Validate()`
- `Validate()` — checks: containers list non-empty, **container names unique** (used as map keys throughout), state names non-empty, events reference defined states (per-container states/events validated independently against their own state set), status check patterns valid
- `CreateConfigFromContainers(...)` — builds a minimal config from a list of `{ID, Name}` pairs (used after interactive selection)

### `internal/docker`

- Wraps `github.com/docker/docker/client`
- `NewClient()` — creates client with `FromEnv` + API version negotiation; opens `docker-client.log`
- `StreamLogs(ctx, containerID, showTail, logChan)` — initial load uses `Tail=50`; reconnections use `Since=now-1s` to avoid re-showing old lines; parses Docker multiplexed stream format (8-byte header per frame)
- `ListContainers(ctx)` — returns all running containers
- `IsContainerRunningByName(ctx, name)` — resolves name → ID at call time, handles container recreation
- `GetContainerIDByName(ctx, name)` — scans running containers by name; returns empty string if not found

### `internal/state`

- `StateManager` — holds `currentState string`, compiled `[]Event{Pattern *regexp.Regexp, State string}`, and `[]StateChangeListener` callbacks
- `ProcessLogEntry(entry)` — iterates events, first match wins, fires listeners in goroutines
- Thread-safe via `sync.RWMutex`; write lock on state change, read lock on queries
- Default initial state is `"healthy"`

### `internal/filter`

- `Filter` — named set of compiled `[]*regexp.Regexp` plus an `exclude bool` flag
  - `exclude: false` (default) — **tag filter**: `GetMatchingFilters` returns its name when any pattern matches, causing the line to be annotated in the UI
  - `exclude: true` — **exclude filter**: `IsExcluded` returns `true` when any pattern matches, causing the line to be silently dropped before reaching the UI
- `Manager` — map of named filters
  - `IsExcluded(entry)` — returns `true` if any exclude-filter matches; checked first in `processLogs`
  - `GetMatchingFilters(entry)` — returns names of all non-exclude filters that match
- Thread-safe via `sync.RWMutex`

### `internal/status`

- `Tracker` — map of `*ServiceStatus` keyed by container name
- `ServiceStatus` — holds `Running bool`, `LastSeen time.Time`, and `CustomStatuses map[string]StatusItem`
- `StatusItem` — `{Label, Status, Value, UpdatedAt}` where `Status` is one of `"ok"`, `"pending"`, `"error"`, `"unknown"`
- Two-level locking: `Tracker.mu` (RWMutex) guards the map; `ServiceStatus.Mu` (RWMutex) guards the struct fields

### `internal/dashboard`

- Central coordinator; owns the `logChan` (capacity 100), `stopChan`, and all per-container maps
- `New(cfg, docker)` — pre-compiles all `StatusCheckPattern`s at construction time
- `Start(ctx)` — sequential init (managers → tracker → UI), then launches goroutines for log processing and container monitoring, then blocks on `ui.Run()`
- `processLogs` — single consumer goroutine draining `logChan`; `entry.ContainerName` is already set to the config entry `Name` by the forwarding goroutine, so no name mapping is performed here; dispatches to state manager, status checks, filter manager, then UI
- `monitorContainerStatus` / `checkAllContainers` — 100 ms ticker; manages stream lifecycle and running-state transitions
- `startLogStreaming` — per-container goroutine; resolves container name to current ID at stream start (handles recreation), creates a **local channel** and a forwarding goroutine that stamps `entry.ContainerName = container.Name` before relaying to `logChan` — this is what allows two config entries to independently monitor the same Docker container, updates status on stream end

### `internal/ui`

**`ui.go` — Main Dashboard UI**

- Built on `github.com/rivo/tview`; all widget mutations must go through `app.QueueUpdateDraw`
- Layout: top panel (Status | State | Filters) + tab bar + log pages
- `ContainerLogView` — per-container: `buffer []string` (max 500 lines), `pendingLines []string` (batch flush optimisation), `pendingNeedsRedraw bool` (triggers full buffer rewrite after trim)
- `updateLoop` goroutine ticks at 500 ms; flushes all pending writes in a single `QueueUpdateDraw`
- State and Filter panels reflect the **currently selected tab** — switching tabs changes which container's managers are queried
- Log export: strips `[color]` tview tags via regex before writing to disk

**`selector.go` — Pre-Dashboard Container Selector**

- Standalone `tview` application shown when no config is available
- Space to toggle, Enter to confirm; returns `[]struct{ID, Name string}` to `main.go`

### `internal/configbuilder`

**`server.go`**

- HTTP server bound to `127.0.0.1:<port>` (loopback only)
- API-only server: registers JSON endpoints for config generation, saved configs, and saved logs
- Routes:
  - `GET /api/config` — returns session metadata (output filename, dirs)
  - `GET /api/lines` — returns parsed log lines with suggested patterns
  - `GET /api/states` — returns available state names
  - `POST /api/generate` — validates drafts, calls `BuildConfigYAML`, optionally saves to `configs/`
  - `GET /api/saved-configs` — lists `*.yaml` in configs dir
  - `GET|PUT|DELETE /api/saved-configs/{name}` — CRUD for saved configs; `?as=drafts` round-trips YAML back to `EventDraft` form
  - `GET /api/saved-logs` — lists `*.log` in logs dir (sorted newest first)
  - `GET /api/saved-logs/{name}` — returns parsed lines + suggested patterns for a log file
- `sanitizeName` — path-traversal protection: strips directory components and disallows characters outside `[a-zA-Z0-9_\-.]`
- `suggestPattern` — escapes a log message as regex, then replaces digit runs with `\d+`

**`logparse.go`**

- `ParseLogFile(path)` — reads lines, strips leading `HH:MM:SS ` timestamp (exactly 9 chars when present), returns `[]LogLine{Raw, Message}`

**`writer.go`**

- `BuildConfigYAML(drafts)` — assembles events and filters from `[]EventDraft`, serialises via `gopkg.in/yaml.v3`, prepends a comment header
- `ParseConfigDrafts(yamlData)` — round-trips saved YAML back to `[]EventDraft` by cross-referencing filter patterns to event patterns

---

## Concurrency Model

```
Goroutines in steady state (per run with N containers):

  main goroutine          — blocked in ui.Run() (tview event loop)
  ui.updateLoop           — 500 ms ticker → QueueUpdateDraw
  dashboard.processLogs   — drains logChan channel
  dashboard.monitorContainerStatus — 100 ms ticker
  N × startLogStreaming   — one per running container (blocked on Docker stream)
  [optional] configbuilder HTTP server goroutine
  [optional] StateChangeListener goroutines — fired on state transitions
```

Shared state and its protection:

| Shared state | Protected by |
|---|---|
| `dashboard.streamingContainers` | `dashboard.streamingMu` (RWMutex) |
| `dashboard.initialLogsLoaded` | `dashboard.initialLogsMu` (RWMutex) |
| `dashboard.containerWasRunning` | `dashboard.containerStateMu` (RWMutex) |
| `status.Tracker.services` map | `Tracker.mu` (RWMutex) |
| `status.ServiceStatus` fields | `ServiceStatus.Mu` (RWMutex) |
| `state.StateManager` fields | `StateManager.mu` (RWMutex) |
| `filter.Manager.filters` map | `Manager.mu` (RWMutex) |
| `filter.Filter.patterns` | `Filter.mu` (RWMutex) |
| `ContainerLogView.buffer` / `pendingLines` | `ContainerLogView.mu` (Mutex) |
| tview widget writes | `app.QueueUpdateDraw` (tview's internal serialisation) |

---

## Configuration Resolution

States, events, and filters follow a **per-container override** pattern:

```
Effective states for container C:
  if len(C.States) > 0  →  use C.States
  else                  →  use config.States (global)

Effective events for container C:
  if len(C.Events) > 0  →  use C.Events
  else                  →  use config.Events (global)

Effective filters for container C:
  if len(C.Filters) > 0 →  use C.Filters
  else                  →  use config.Filters (global)
```

Each container gets its own independent `StateManager` and filter `Manager` instance, so state machines are fully isolated between containers.

---

## Debug Log Files

Both `dashboard.go` and `docker/client.go` write timestamped debug logs to files in the working directory:

| File | Written by | Contents |
|---|---|---|
| `dashboard.log` | `internal/dashboard` | Stream start/stop events, container state transitions, log count milestones |
| `docker-client.log` | `internal/docker` | Docker API calls, frame parsing errors, stream lifecycle |

These files are opened in append mode at startup and are never rotated by the application.

---

## Testing

Unit tests live alongside their packages in `*_test.go` files. There are no integration tests (Docker not required).

| Package | Test file | What is covered |
|---|---|---|
| `internal/config` | `config_test.go` | `Validate` (all error paths + valid cases), `StatusCheckPattern.Compile` (all 4 match types, multi-term, ignore-case), `TextList.UnmarshalYAML`, `Load` |
| `internal/state` | `manager_test.go` | State transitions, first-match-wins, no-transition-when-same-state, `StateChangeListener` delivery, `GetStateDescription` |
| `internal/filter` | `filter_test.go` | `GetMatchingFilters`, `IsExcluded`, exclude vs include, empty patterns, `GetFilter`/`AllFilters` |
| `internal/status` | `tracker_test.go` | `RegisterService` idempotency, `UpdateServiceRunning`, custom status CRUD, unknown-service no-ops, concurrent access |
| `internal/configbuilder` | `configbuilder_test.go` | `extractMessage`, `sanitizeName` (path traversal), `suggestPattern`, `BuildConfigYAML`, `ParseConfigDrafts` round-trip, `ParseLogFile`, all HTTP handlers via `httptest` |

The `config-validator` binary is intentionally thin and reuses `internal/config.Load()`, so validator behavior always matches what the dashboard itself will accept or reject.

```bash
# Run all tests
go test ./...

# Without cache
go test -count=1 ./...

# With race detector
go test -race ./...

# Benchmarks
go test ./internal/... -bench=. -benchmem

# CPU/memory profiles from a benchmark run
go test ./internal/state/ -bench=. -cpuprofile=cpu.prof -memprofile=mem.prof
go tool pprof -http=:8080 cpu.prof
```
