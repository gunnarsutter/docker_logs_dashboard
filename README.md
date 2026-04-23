# Docker Logs Dashboard

A powerful full-screen terminal UI application for monitoring Docker container logs with support for filtering, pattern matching, and state-based event triggering.

The repository also includes two supporting CLI tools:
- `config-builder`: API server for generating config snippets from exported logs
- `config-validator`: standalone config validation CLI

## Features

- **Full-Screen Terminal UI**: Beautiful, real-time terminal interface with status panels
- **No-Config Mode**: Run without a config file — the dashboard lists all running Docker containers and lets you pick which ones to monitor interactively
- **Service Status Tracking**: Monitor container health and custom status indicators
  - Running/stopped status for each container
  - Connection status (e.g., Zenoh connectivity)
  - Data reception status (e.g., RCS information)
- **Multi-Container Monitoring**: Monitor logs from multiple Docker containers simultaneously with tabbed views — including multiple independent views of the **same** container with different filters or state machines
- **Pattern-Based Filtering**: Define custom filters using regex patterns to tag specific log entries, or suppress unwanted noise with exclude filters
- **State Management**: Define system states and events that trigger state transitions based on log patterns — globally or per container
- **Real-Time Streaming**: Live log streaming with automatic reconnection
- **Container Restart Recovery**: Automatically detects and reconnects to restarted or recreated containers (tracked by name, not ID)
- **Log Export**: Save buffered logs for any container to a file for later analysis or config generation
- **Per-Container Configuration**: States, events, and filters can be defined globally or overridden on a per-container basis
- **Flexible Configuration**: YAML-based configuration for easy customization

## UI Layout

```
┌─ System Status ──────────────────┬─ Current State ───┬─ Active Filters ───┐
│ ● DataCollector | Zenoh: Connected     │ State: OPERATING  │ Total: 8 filters   │
│ ● zenoh   | RCS: Data received   │                   │ • startup-sequence │
│           |                      │ Description...    │ • connection-events│
└──────────────────────────────────┴───────────────────┴────────────────────┘
┌─ DataCollector Logs ────────────────────┬─ zenoh Logs ──────────────────────────┐
│ 15:04:05 [connection-events]      │ 15:04:02 Initial conf...              │
│   ... connected!                  │ 15:04:03 Using ZID: b4ebccbe...       │
│ 15:04:10 [data-loading]           │ 15:04:04 Zenoh can be reached at:     │
│   Added 178 parameters            │   tcp/172.18.0.2:7447                 │
│ 15:04:12 ► STATE CHANGE:          │ 15:04:05 listening scout messages     │
│   connecting → operational        │   on 224.0.0.224:7446                 │
│ ...                               │ ...                                   │
└───────────────────────────────────┴───────────────────────────────────────┘
```

Each container gets its own dedicated log panel, making it easy to:
- Track logs from multiple containers independently
- See relevant logs without mixed output
- Identify patterns specific to each service

### Status Indicators

- **Green ●**: Container is running
- **Red ●**: Container is stopped or not found
- **Status Colors**:
  - Green: OK/Connected/Success
  - Yellow: Pending/Connecting/In Progress
  - Red: Error/Failed
  - Gray: Unknown/Not Available

## Installation

### Prerequisites

- Go 1.21 or later
- Docker daemon running and accessible
- Docker API access (usually via `/var/run/docker.sock`)

### Build from Source

```bash
# Clone or navigate to the repository
cd docker-logs-dashboard

# Download dependencies
go mod download

# Build the application
go build -o docker-logs-dashboard .

# Make it executable (Linux/Mac)
chmod +x docker-logs-dashboard
```

## Configuration

Create a `config.yaml` file based on the provided `config.example.yaml`:

```yaml
# Define containers to monitor
containers:
  - name: "web-server"
    container_id: "web-server-container"
  
  - name: "database"
    container_id: "postgres-db"

# Global states — used by any container that does not define its own
states:
  - name: "healthy"
    description: "System is operating normally"
  
  - name: "error"
    description: "Errors detected in logs"

# Global events — used by any container that does not define its own
events:
  - name: "error-detected"
    pattern: "(?i)(error|exception|failed)"
    state: "error"
  
  - name: "health-check"
    pattern: "(?i)(ready|healthy|started successfully)"
    state: "healthy"

# Global filters — used by any container that does not define its own
filters:
  - name: "errors-only"
    description: "Show only error messages"
    patterns:
      - "(?i)(error|exception|failed)"
  
  - name: "api-requests"
    description: "Filter API request logs"
    patterns:
      - "(?i)(GET|POST|PUT|DELETE|PATCH)"
```

### Per-Container States, Events, and Filters

States, events, and filters can be defined directly on a container, which **overrides** the global definitions for that container. This lets you track different state machines or apply container-specific filters without affecting others.

```yaml
containers:
  - name: "web-server"
    container_id: "web-server-container"
    # This container uses the global states/events/filters (none defined here)

  - name: "worker"
    container_id: "my-worker-1"
    # Container-specific states (replaces globals for this container)
    states:
      - name: "idle"
        description: "Worker is waiting for jobs"
      - name: "processing"
        description: "Worker is processing a job"
      - name: "error"
        description: "Worker encountered an error"
    # Container-specific events
    events:
      - name: "job-started"
        pattern: "(?i)processing job"
        state: "processing"
      - name: "job-done"
        pattern: "(?i)job complete"
        state: "idle"
      - name: "job-failed"
        pattern: "(?i)(error|failed)"
        state: "error"
    # Container-specific filters
    filters:
      - name: "job-events"
        description: "Show job lifecycle events"
        patterns:
          - "(?i)(processing job|job complete|job failed)"
      - name: "noise"
        description: "Suppress heartbeat spam"
        exclude: true
        patterns:
          - "heartbeat"

# Global fallback states/events/filters (used by containers without their own)
states:
  - name: "healthy"
    description: "System is operating normally"
  - name: "error"
    description: "Errors detected"
events:
  - name: "error-detected"
    pattern: "(?i)(error|exception|failed)"
    state: "error"
filters: []
```

> **Resolution order**: if a container defines its own `states`, `events`, or `filters`, those are used exclusively for that container. The global definitions are used only for containers that do not define their own.

### Configuration Options

#### Containers
- `name`: A friendly name for the container
- `container_id`: The Docker container name or ID
- `states` *(optional)*: Per-container state definitions — overrides global `states` for this container
- `events` *(optional)*: Per-container event definitions — overrides global `events` for this container
- `filters` *(optional)*: Per-container filter definitions — overrides global `filters` for this container
- `status_checks` *(optional)*: Log-pattern-driven status indicators shown in the System Status panel
- `retain_logs_on_restart` *(optional, default true)*: Whether to keep buffered logs when the container restarts

#### States (global or per-container)
- `name`: State identifier
- `description`: Human-readable description

#### Events (global or per-container)
- `name`: Event identifier
- `pattern`: Regex pattern to match in logs
- `state`: State to transition to when pattern matches

#### Filters (global or per-container)
- `name`: Filter identifier
- `description`: Human-readable description
- `exclude` *(optional, default false)*: When `true`, any log line matching these patterns is **silently dropped** and never shown in the log view. When `false` (default), matching lines are **tagged** with the filter name in blue brackets.
- `patterns`: List of regex patterns (any single match triggers the filter)

## Usage

### Basic Usage

```bash
# Run with default config file (configs/config.yaml)
./docker-logs-dashboard

# Skip config and choose containers interactively
./docker-logs-dashboard -no-config

# Specify a custom config file
./docker-logs-dashboard -config myconfig.yaml

# Specify a custom configs directory
./docker-logs-dashboard -configs-dir /path/to/configs

# Also start the config-builder API server (APIs only, no web UI)
./docker-logs-dashboard -api
./docker-logs-dashboard -api -web-port 9090

# Validate a config file without starting the dashboard
go run ./cmd/config-validator -config config.datacollector.yaml
```

### Config Validator

Use the validator to check a config file and get a clear error without launching the TUI:

```bash
# Validate the default config path (resolved via configs/ when relative)
go run ./cmd/config-validator

# Validate a specific config
go run ./cmd/config-validator -config config.datacollector-sparkplug.yaml

# Validate using a custom configs directory
go run ./cmd/config-validator -config myconfig.yaml -configs-dir /path/to/configs

# Only print valid/invalid, no summary
go run ./cmd/config-validator -summary=false -config myconfig.yaml

# Emit JSON for CI/scripts
go run ./cmd/config-validator -format json -config myconfig.yaml

# Shell-friendly mode: no output, exit code only
go run ./cmd/config-validator -quiet -config myconfig.yaml

# Example shell usage
go run ./cmd/config-validator -quiet -config myconfig.yaml && echo ok || echo invalid

# Makefile shortcut
make validate-config CONFIG=config.datacollector-sparkplug.yaml

# Quiet Makefile mode
make validate-config CONFIG=myconfig.yaml QUIET=1
```

### No-Config / Interactive Container Selection

If no config file is found, or if you pass `-no-config`, the dashboard shows an interactive container selector. This is perfect for quick exploration or when you don't want to create a full config file.

#### How It Works

1. **Start without a config**:
   ```bash
   ./docker-logs-dashboard
   # or explicitly
   ./docker-logs-dashboard -no-config
   ```

2. **Container Selector UI appears** showing:
   - All running Docker containers
   - Container names and current state (running/paused/etc.)
   - Status information
   - Visual checkmarks (✓) for selected containers

3. **Select your containers**:
   - **Arrow Up/Down**: Navigate the list
   - **Space**: Toggle selection (✓ marks selected containers)
   - **Enter**: Confirm selection and start the dashboard
   - **Esc**: Cancel and exit

#### What You Get

The dashboard will monitor your selected containers with:
- **Raw log display**: All logs shown without filtering or state tracking (since no config was provided)
- **Tab switching**: Use 1–9/0 keys to switch between selected containers
- **Log export**: Press `e` to save buffered logs to files for analysis
- **Container restart recovery**: Automatically reconnects if containers are restarted

#### Next Steps

Export logs using the `e` key, then use the config-builder tool to:
- Analyze the logs
- Define custom filters and state machines
- Generate a full configuration file for future runs

See [Config Builder](#config-builder) section for details.

### Keyboard Controls

#### Container Selection (No-Config Mode)
| Key | Action |
|-----|--------|
| **↑/↓** | Navigate container list |
| **Space** | Toggle selection |
| **Enter** | Start dashboard with selected containers |
| **Esc** | Cancel and exit |

#### Dashboard Display
| Key | Action |
|-----|--------|
| **1–9, 0** | Switch to container tab 1–10 |
| **↑ / ↓** | Scroll up/down through logs |
| **Page Up / Page Down** | Scroll logs one page at a time |
| **e** or **E** | Export current container's logs to a file |
| **h** | Toggle keyboard shortcuts legend |
| **c** or **C** | Clear current container's buffered logs |
| **Ctrl+C** | Exit the application |

### Log Export

Press **`e`** while viewing any container tab to save its buffered logs to disk.

Files are saved to the home directory as:
```
~/logs/<container-name>.<datetime>.log
```
For example: `~/logs/datacollector.2026-04-15T10-30-45.log`

Color/formatting codes are stripped, producing clean plain-text files. These files can be loaded into the config-builder web UI to create pattern-based config files.

The config-builder web UI automatically discovers all `.log` files in `~/logs/` in the **Logs** tab, allowing you to load exported logs without restarting the server.

### Understanding the Display

The application displays three main sections:

1. **System Status Panel** (top-left): Shows running status (● indicator) and custom metrics for each monitored service
2. **Current State Panel** (top-center): Displays the state of the currently selected container tab and its description
3. **Active Filters Panel** (top-right): Lists all filters configured for the currently selected container tab
4. **Container Logs** (bottom): Real-time log stream with color-coded filter tags

### Example Session

When monitoring containers, you'll see:

1. **Initial startup**: Containers show as running (green ●) or stopped (red ●)
2. **Custom statuses**: Any configured custom metrics appear directly in the System Status Panel
3. **State transitions**: System state changes in the Current State Panel
4. **Log filtering**: All logs are tagged with matching filters in blue brackets, making it easy to identify important events

## Docker Permissions

To access the Docker daemon, you need appropriate permissions:

### Linux

Add your user to the docker group:
```bash
sudo usermod -aG docker $USER
# Log out and back in for changes to take effect
```

Or run with sudo:
```bash
sudo ./docker-logs-dashboard
```

### macOS

Docker Desktop should work out of the box.

### Using Docker Socket

The application connects to Docker via the default socket at `/var/run/docker.sock`. If you're using a different socket location, set the `DOCKER_HOST` environment variable:

```bash
export DOCKER_HOST=unix:///path/to/docker.sock
./docker-logs-dashboard
```

## Development

### Project Structure

```
docker-logs-dashboard/
├── main.go                          # TUI entry point
├── go.mod
├── Makefile
├── cmd/
│   └── config-builder/
│       └── main.go                  # Standalone config-builder entry point
│   └── config-validator/
│       └── main.go                  # Standalone config-validator entry point
├── configs/                         # Saved YAML configs
├── internal/
│   ├── config/
│   │   ├── config.go                # YAML config loader & validator
│   │   └── config_test.go
│   ├── docker/
│   │   └── client.go                # Docker API client wrapper
│   ├── filter/
│   │   ├── filter.go                # Log filter engine
│   │   └── filter_test.go
│   ├── state/
│   │   ├── manager.go               # Per-container state machine
│   │   └── manager_test.go
│   ├── status/
│   │   ├── tracker.go               # Service status tracker
│   │   └── tracker_test.go
│   ├── dashboard/
│   │   └── dashboard.go             # Orchestration coordinator
│   ├── ui/
│   │   ├── ui.go                    # Main TUI (tview)
│   │   └── selector.go              # Interactive container selector
│   └── configbuilder/
│       ├── server.go                # HTTP API server & handlers
│       ├── logparse.go              # Log file parser
│       ├── writer.go                # YAML generation
│       ├── configbuilder_test.go
│       └── static/
│           └── index.html           # Config builder web UI
```

### Adding New Features

1. **Adding New Filter Types**: Modify `internal/filter/filter.go`
2. **Adding New Event Types**: Update the configuration schema in `internal/config/config.go`
3. **Custom Log Processors**: Extend `internal/dashboard/dashboard.go`

## Testing

```bash
# Run all tests
go test ./...

# Run without cache
go test -count=1 ./...

# Run with race detection
go test -race ./...

# Run with coverage
go test -cover ./...

# Run a specific package verbosely
go test ./internal/state/ -v

# Run benchmarks
go test ./internal/... -bench=. -benchmem

# Build the validator
make build-config-validator

# Validate a config via Makefile
make validate-config CONFIG=config.datacollector-sparkplug.yaml
```

Unit tests cover: `config`, `state`, `filter`, `status`, and `configbuilder` (including HTTP handler tests via `net/http/httptest`).

## Troubleshooting

### Container Not Found

If you see "container not found" errors, verify:
- The container is running: `docker ps`
- The container name/ID in config.yaml matches exactly
- You have permissions to access Docker

### Permission Denied

If you get permission errors:
- Ensure Docker socket is accessible
- Add your user to the docker group (Linux)
- Try running with sudo

### No Logs Appearing

- Check that the containers are producing logs
- Verify your filter patterns aren't too restrictive
- Check that the Docker API is accessible

## Regex Pattern Examples

```yaml
# Match errors (case-insensitive)
pattern: "(?i)(error|exception|failed)"

# Match HTTP methods
pattern: "(GET|POST|PUT|DELETE|PATCH)\\s+/\\S+"

# Match IP addresses
pattern: "\\b(?:[0-9]{1,3}\\.){3}[0-9]{1,3}\\b"

# Match timestamps
pattern: "\\d{4}-\\d{2}-\\d{2}\\s+\\d{2}:\\d{2}:\\d{2}"

# Match SQL queries
pattern: "(?i)(SELECT|INSERT|UPDATE|DELETE)\\s+.*\\s+FROM"
```

## License

MIT License - feel free to use and modify as needed.

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.
