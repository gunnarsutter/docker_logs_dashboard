# How to Run the TUI Dashboard

The Docker Logs Dashboard now features a full-screen terminal user interface!

## Quick Start

```bash
# Make sure your containers are running
docker ps

# Run the dashboard (with automatic container selection)
./docker-logs-dashboard

# Or with a specific config file
./docker-logs-dashboard -config config.yaml

# Force interactive container selection
./docker-logs-dashboard -no-config
```

## Running Without a Config (No-Config Mode)

If no config file exists, the dashboard automatically starts in **no-config mode** with an interactive container selector:

### Container Selector

You'll see a terminal list of all running containers:

```
┌─ Select Containers ──────────────────────┐
│ ✓ webapp          (running)              │
│   db              (running)              │
│ ✓ cache           (running)              │
│   backup          (paused)               │
│                                          │
│ Arrow keys: navigate                     │
│ Space: toggle  Enter: confirm  Esc: exit│
│                                          │
│ Selected: 2 containers                   │
└──────────────────────────────────────────┘
```

**Controls:**
- **↑/↓ Arrow Keys**: Navigate through the container list
- **Space**: Toggle selection (checkmark appears)
- **Enter**: Start dashboard with selected containers
- **Esc**: Exit without starting

### No-Config Dashboard Features

Once started, the dashboard works like the normal mode except:
- No status checks are performed (no config-defined checks)
- No states or events are tracked (no state machine defined)
- No filters are applied (view raw logs)
- Focus is on viewing raw container output

### Typical Workflow

1. Run `./docker-logs-dashboard` to start in no-config mode
2. Select the containers you want to monitor
3. View raw logs from your selected containers
4. Export logs using the `e` key for specific containers
5. Use exported logs with the **config-builder** tool to create a proper config
6. Future runs use `./docker-logs-dashboard -config myconfig.yaml`

## What You'll See

### Top Panel - System Status
Shows real-time status for each monitored container:
- **DataCollector**: 
  - Running indicator (green/red dot)
  - Zenoh connection status (Connecting/Connected/Failed)
  - RCS data reception status (Waiting/Data received)
- **zenoh**:
  - Running indicator

### Middle Panel - Current State
Displays the overall system state:
- initializing → connecting → operational → degraded → critical

### Logs Panels (Side by Side)
Each container gets its own dedicated log view:
- **DataCollector Logs** (left panel): Shows only DataCollector container logs
- **Zenoh Logs** (right panel): Shows only zenoh container logs

Each log view displays:
- Timestamps
- Filter tags (in blue brackets)
- Color-coded messages
- Auto-scrolling to latest entries

## Automatic Status Tracking

The dashboard automatically detects and updates:

1. **Zenoh Connection** (for DataCollector):
   - Detects "Connecting to Zenoh" → status becomes "Connecting..."
   - Detects "... connected!" → status becomes "Connected" (green)
   - Detects "Failed to connect" → status becomes "Connection failed" (red)

2. **RCS Information** (for DataCollector):
   - Detects "Waiting for RCS" → status is "Waiting"
   - Detects "Requesting RCS" → status is "Requesting..."
   - Detects "Added X parameters to database" → status becomes "Data received" (green)

## Keyboard Controls

### Container Selection (No-Config Mode)
| Key | Action |
|-----|--------|
| **↑/↓** | Navigate container list |
| **Space** | Toggle container selection |
| **Enter** | Confirm selection and start dashboard |
| **Esc** | Cancel and exit |

### Dashboard Display
| Key | Action |
|-----|--------|
| **1–9, 0** | Switch between container tabs |
| **↑/↓** | Scroll logs up/down |
| **Page Up/Down** | Scroll logs by page |
| **e** or **E** | Export current container's logs to `~/logs/` |
| **h** | Toggle keyboard shortcuts legend (footer) |
| **Ctrl+C** | Exit the dashboard cleanly |

## Tips

- The UI auto-refreshes every 500ms
- Logs auto-scroll to show latest entries (when at bottom)
- State changes appear as special highlighted messages (with config)
- Press Ctrl+C to exit cleanly
- Use log export (`e` key) to analyze container output and generate configs
- No-config mode is great for exploring containers before creating a full config

## Working With Configs

For detailed info on creating and using config files, see the [main README.md](README.md).

### Creating a Config from Logs

1. Use the **config-builder** API server to parse exported logs
2. See the [Config Builder](#../README.md#config-builder) section in README
3. Generate YAML config files with custom filters and state machines

## Customizing Status Tracking

To add custom status tracking for other patterns, edit:
`internal/dashboard/dashboard.go` in the `processLogs()` function.

Example:
```go
if regexp.MustCompile(`your-pattern`).MatchString(entry.Message) {
    d.statusTracker.UpdateCustomStatus("service-name", "key", "Label", "ok", "Value")
}
```

Status types: `"ok"` (green), `"pending"` (yellow), `"error"` (red), `"unknown"` (gray)
