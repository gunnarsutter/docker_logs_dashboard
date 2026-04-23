package dashboard

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
	"docker-logs-dashboard/internal/filter"
	"docker-logs-dashboard/internal/state"
	"docker-logs-dashboard/internal/status"
	"docker-logs-dashboard/internal/ui"
)

// Dashboard coordinates log monitoring, filtering, and state management
type Dashboard struct {
	config         *config.Config
	dockerClient   *docker.Client
	stateManagers  map[string]*state.StateManager // keyed by container config name
	filterManagers map[string]*filter.Manager     // keyed by container config name
	statusTracker  *status.Tracker
	ui             *ui.UI
	logChan        chan docker.LogEntry
	stopChan       chan struct{}
	wg             sync.WaitGroup
	logger         *log.Logger

	// Track which containers are currently streaming
	streamingContainers map[string]bool
	streamingMu         sync.RWMutex

	// Track which containers have had their initial logs loaded (to avoid re-showing tail)
	initialLogsLoaded map[string]bool
	initialLogsMu     sync.RWMutex

	// Track last known running state to detect transitions
	containerWasRunning map[string]bool
	containerStateMu    sync.RWMutex

	// Compiled status checks from config, keyed by container config name
	compiledStatusChecks map[string][]compiledStatusCheck
}

type compiledStatusCheck struct {
	key      string
	label    string
	patterns []config.CompiledStatusCheckPattern
}

// New creates a new dashboard instance
func New(cfg *config.Config, dockerClient *docker.Client) *Dashboard {
	// Create log file for debugging
	logFile, err := os.OpenFile("dashboard.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		panic(fmt.Sprintf("failed to create log file: %v", err))
	}

	logger := log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)
	logger.Println("=== Dashboard instance created ===")

	compiledChecks := make(map[string][]compiledStatusCheck)
	for _, c := range cfg.Containers {
		var checks []compiledStatusCheck
		for _, sc := range c.StatusChecks {
			var patterns []config.CompiledStatusCheckPattern
			for _, p := range sc.Patterns {
				patterns = append(patterns, p.Compile())
			}
			checks = append(checks, compiledStatusCheck{
				key:      sc.Key,
				label:    sc.Label,
				patterns: patterns,
			})
		}
		if len(checks) > 0 {
			compiledChecks[c.Name] = checks
		}
	}

	return &Dashboard{
		config:               cfg,
		dockerClient:         dockerClient,
		logChan:              make(chan docker.LogEntry, 100),
		stopChan:             make(chan struct{}),
		streamingContainers:  make(map[string]bool),
		initialLogsLoaded:    make(map[string]bool),
		containerWasRunning:  make(map[string]bool),
		logger:               logger,
		compiledStatusChecks: compiledChecks,
		stateManagers:        make(map[string]*state.StateManager),
		filterManagers:       make(map[string]*filter.Manager),
	}
}

// Start begins monitoring containers
func (d *Dashboard) Start(ctx context.Context) error {
	// Initialize per-container state and filter managers.
	// Per-container states/events/filters take precedence; fall back to globals.
	for _, container := range d.config.Containers {
		states := container.States
		if len(states) == 0 {
			states = d.config.States
		}
		events := container.Events
		if len(events) == 0 {
			events = d.config.Events
		}
		sm, err := state.NewStateManager(states, events)
		if err != nil {
			return fmt.Errorf("failed to initialize state manager for container '%s': %w", container.Name, err)
		}
		d.stateManagers[container.Name] = sm

		filters := container.Filters
		if len(filters) == 0 {
			filters = d.config.Filters
		}
		fm, err := filter.NewManager(filters)
		if err != nil {
			return fmt.Errorf("failed to initialize filter manager for container '%s': %w", container.Name, err)
		}
		d.filterManagers[container.Name] = fm
	}

	// Initialize status tracker
	d.statusTracker = status.NewTracker()

	// Register services
	for _, container := range d.config.Containers {
		d.statusTracker.RegisterService(container.Name)

		// Initialize all containers with a default status
		d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "pending", "Initializing...")

		// Initialize custom statuses from config
		for _, sc := range container.StatusChecks {
			d.statusTracker.UpdateCustomStatus(container.Name, sc.Key, sc.Label, sc.InitialStatus, sc.InitialValue)
		}
	}

	// Initialize UI
	d.ui = ui.New(d.statusTracker, d.stateManagers, d.filterManagers, d.config.Containers)

	// Set up logs directory for exported logs (~/logs/)
	homeDir, err := os.UserHomeDir()
	if err == nil {
		logsDir := filepath.Join(homeDir, "logs")
		if err := os.MkdirAll(logsDir, 0755); err == nil {
			d.ui.SetLogsDir(logsDir)
		}
	}

	// Add a no-op state change listener for each container (extend here to act on transitions)
	for _, sm := range d.stateManagers {
		sm.AddStateChangeListener(func(from, to string, event string, containerName string, timestamp time.Time) {
			_ = from
			_ = to
			_ = event
			_ = containerName
			_ = timestamp
		})
	}

	// Start log processor
	d.wg.Add(1)
	go d.processLogs(ctx)

	// Start container status monitor (this will start log streaming for running containers)
	d.wg.Add(1)
	go d.monitorContainerStatus(ctx)

	// Start UI (blocking)
	if err := d.ui.Run(); err != nil {
		return fmt.Errorf("UI error: %w", err)
	}

	// UI stopped (user pressed Ctrl+C), clean up
	close(d.stopChan)
	d.wg.Wait()

	return nil
}

// processLogs processes incoming log entries
func (d *Dashboard) processLogs(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopChan:
			return
		case entry := <-d.logChan:
			// ContainerName is pre-set to the config entry Name by startLogStreaming,
			// so no mapping is needed here.

			// Process for state changes (use per-container manager)
			if sm, ok := d.stateManagers[entry.ContainerName]; ok {
				sm.ProcessLogEntry(entry)
			}

			// Apply configured status checks
			if checks, ok := d.compiledStatusChecks[entry.ContainerName]; ok {
				for _, check := range checks {
					for _, p := range check.patterns {
						if p.Matches(entry.Message) {
							d.statusTracker.UpdateCustomStatus(entry.ContainerName, check.key, check.label, p.Status, p.Value)
							break
						}
					}
				}
			}

			// Check which filters match (use per-container manager)
			var matchingFilters []string
			if fm, ok := d.filterManagers[entry.ContainerName]; ok {
				if fm.IsExcluded(entry) {
					continue
				}
				matchingFilters = fm.GetMatchingFilters(entry)
			}

			// Send to UI
			d.ui.AddLog(entry, matchingFilters)
		}
	}
}

// GetCurrentState returns the current state for the first container, or "unknown".
func (d *Dashboard) GetCurrentState() string {
	for _, container := range d.config.Containers {
		if sm, ok := d.stateManagers[container.Name]; ok {
			return sm.GetCurrentState()
		}
	}
	return "unknown"
}

// startLogStreaming starts streaming logs for a specific container
func (d *Dashboard) startLogStreaming(ctx context.Context, container config.Container) {
	d.streamingMu.Lock()
	// Check if already streaming
	if d.streamingContainers[container.Name] {
		d.logger.Printf("[%s/%s] Already streaming, skipping", container.Name, container.ContainerID[:12])
		d.streamingMu.Unlock()
		return
	}
	d.streamingContainers[container.Name] = true
	d.streamingMu.Unlock()

	// Check if this is the first time loading logs
	d.initialLogsMu.RLock()
	showTail := !d.initialLogsLoaded[container.Name]
	d.initialLogsMu.RUnlock()

	d.logger.Printf("[%s/%s] Starting log stream - showTail=%v", container.Name, container.ContainerID[:12], showTail)

	// Mark as initially loaded
	if showTail {
		d.initialLogsMu.Lock()
		d.initialLogsLoaded[container.Name] = true
		d.initialLogsMu.Unlock()
		d.logger.Printf("[%s/%s] Marked as initially loaded", container.Name, container.ContainerID[:12])
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.streamingMu.Lock()
			d.streamingContainers[container.Name] = false
			d.streamingMu.Unlock()
			d.logger.Printf("[%s] Stream ended, streaming flag set to false", container.Name)
		}()

		// Resolve the current container ID by name — this handles the case where
		// the container was recreated and has a new ID
		currentID, err := d.dockerClient.GetContainerIDByName(ctx, container.ContainerID)
		if err != nil || currentID == "" {
			d.logger.Printf("[%s] Could not resolve container ID by name: %v", container.Name, err)
			return
		}
		d.logger.Printf("[%s] Resolved current container ID: %s", container.Name, currentID[:12])

		// Use a local channel so we can tag each entry with this config entry's
		// Name before it reaches the shared logChan. This allows multiple config
		// entries to reference the same Docker container independently.
		localChan := make(chan docker.LogEntry, 100)
		fwdDone := make(chan struct{})
		go func() {
			defer close(fwdDone)
			for {
				select {
				case entry, ok := <-localChan:
					if !ok {
						return
					}
					entry.ContainerName = container.Name
					select {
					case d.logChan <- entry:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		streamErr := d.dockerClient.StreamLogs(ctx, currentID, showTail, localChan)
		close(localChan)
		<-fwdDone

		// Check if stream ended due to context cancellation or other reason
		if ctx.Err() != nil {
			d.logger.Printf("[%s] Stream ended due to context cancellation", container.Name)
			return
		}

		// Stream ended unexpectedly - check container status immediately
		if streamErr != nil {
			d.logger.Printf("[%s] Stream error: %v", container.Name, streamErr)
		} else {
			d.logger.Printf("[%s] Stream ended normally (no error)", container.Name)
		}

		d.logger.Printf("[%s] Checking container status after stream ended", container.Name)
		isRunning, checkErr := d.dockerClient.IsContainerRunningByName(ctx, container.Name)
		if checkErr != nil {
			d.logger.Printf("[%s] Error checking container after stream end: %v", container.Name, checkErr)
			d.statusTracker.UpdateServiceRunning(container.Name, false)
			d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "error", "Connection lost")
		} else {
			d.statusTracker.UpdateServiceRunning(container.Name, isRunning)
			if isRunning {
				d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "ok", "Running")
				d.logger.Printf("[%s] Container still running, will restart stream", container.Name)
			} else {
				d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "pending", "Stopped")
				d.logger.Printf("[%s] Container stopped", container.Name)
			}
		}
	}()
}

// monitorContainerStatus periodically checks container status and manages log streaming
func (d *Dashboard) monitorContainerStatus(ctx context.Context) {
	defer d.wg.Done()

	ticker := time.NewTicker(100 * time.Millisecond) // Check every 100 milliseconds
	defer ticker.Stop()

	// Do an initial check immediately
	d.checkAllContainers(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopChan:
			return
		case <-ticker.C:
			d.checkAllContainers(ctx)
		}
	}
}

// checkAllContainers checks the status of all configured containers
func (d *Dashboard) checkAllContainers(ctx context.Context) {
	for _, container := range d.config.Containers {
		isRunning, err := d.dockerClient.IsContainerRunningByName(ctx, container.ContainerID)
		if err != nil {
			// Container not found or error checking
			d.logger.Printf("[%s/%s] Error checking if running: %v", container.Name, container.ContainerID[:12], err)
			d.statusTracker.UpdateServiceRunning(container.Name, false)
			d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "error", "Not found")
			continue
		}

		d.containerStateMu.RLock()
		wasRunning := d.containerWasRunning[container.Name]
		d.containerStateMu.RUnlock()

		if isRunning {
			// Container is running
			d.statusTracker.UpdateServiceRunning(container.Name, true)
			d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "ok", "Running")

			// Notify log view if it just came back up after being down
			if !wasRunning {
				d.containerStateMu.Lock()
				d.containerWasRunning[container.Name] = true
				d.containerStateMu.Unlock()
				if d.ui != nil {
					retainLogs := container.RetainLogsOnRestart == nil || *container.RetainLogsOnRestart
					if !retainLogs {
						d.ui.ClearContainerLog(container.Name)
					}
					d.ui.AddStatusMessageToContainer("Container is back up - resuming log stream", container.Name)
				}
			}

			// Check if we need to start log streaming
			d.streamingMu.RLock()
			streaming := d.streamingContainers[container.Name]
			d.streamingMu.RUnlock()

			if !streaming {
				// Container is running but not streaming - start streaming
				d.logger.Printf("[%s/%s] Container running but not streaming, starting stream", container.Name, container.ContainerID[:12])
				// Status will be shown in System Status window
				d.startLogStreaming(ctx, container)
			} else {
				// Already streaming, all good
				// d.logger.Printf("[%s/%s] Container running and streaming", container.Name, container.ContainerID[:12])
			}
		} else {
			// Container exists but is not running
			d.logger.Printf("[%s/%s] Container stopped", container.Name, container.ContainerID[:12])
			d.statusTracker.UpdateServiceRunning(container.Name, false)
			d.statusTracker.UpdateCustomStatus(container.Name, "status", "Status", "pending", "Stopped")

			// Emit a message into the log view only on transition (running → stopped)
			if wasRunning {
				d.containerStateMu.Lock()
				d.containerWasRunning[container.Name] = false
				d.containerStateMu.Unlock()
				if d.ui != nil {
					d.ui.AddStatusMessageToContainer("Container is not running", container.Name)
				}
			}
		}
	}
}
