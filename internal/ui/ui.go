package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/docker"
	"docker-logs-dashboard/internal/filter"
	"docker-logs-dashboard/internal/state"
	"docker-logs-dashboard/internal/status"
)

// ContainerLogView represents a log view for a specific container
type ContainerLogView struct {
	view         *tview.TextView
	buffer       []string
	latestStatus string
	mu           sync.Mutex

	// Pending lines are accumulated between draw ticks and flushed in a single QueueUpdateDraw
	pendingLines       []string
	pendingNeedsRedraw bool
}

// UI represents the terminal user interface
type UI struct {
	app          *tview.Application
	statusBar    *tview.TextView
	logViews     map[string]*ContainerLogView
	stateView    *tview.TextView
	filterView   *tview.TextView
	shortcutsBar *tview.TextView
	bottomPanel  *tview.Flex
	layout       *tview.Flex
	logPages     *tview.Pages
	tabBar       *tview.TextView

	statusTracker  *status.Tracker
	stateManagers  map[string]*state.StateManager
	filterManagers map[string]*filter.Manager
	containers     []config.Container

	maxLogLines   int
	currentTabIdx int
	logsDir       string
	showShortcuts bool
}

const shortcutsLegend = "[aqua]1-9/0[white] tabs  [aqua]Arrows[white] scroll  [aqua]PgUp/PgDn[white] page  [yellow]e[white] export  [yellow]c[white] clear  [red]Ctrl+C[white] quit"

// SetLogsDir configures the directory where exported logs are written.
func (ui *UI) SetLogsDir(dir string) {
	ui.logsDir = dir
}

// SetMaxLogLines configures the number of buffered log lines retained per container.
func (ui *UI) SetMaxLogLines(max int) {
	if max > 0 {
		ui.maxLogLines = max
	}
}

// New creates a new UI instance
func New(statusTracker *status.Tracker, stateManagers map[string]*state.StateManager, filterManagers map[string]*filter.Manager, containers []config.Container) *UI {
	app := tview.NewApplication()

	ui := &UI{
		app:            app,
		statusTracker:  statusTracker,
		stateManagers:  stateManagers,
		filterManagers: filterManagers,
		containers:     containers,
		logViews:       make(map[string]*ContainerLogView),
		maxLogLines:    500,
		showShortcuts:  false,
	}

	ui.setupUI()
	return ui
}

func (ui *UI) setupUI() {
	// Status bar at the top
	ui.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	ui.statusBar.
		SetBorder(true).
		SetTitle(" System Status ").
		SetBorderColor(tcell.ColorBlue)

	// Current state view
	ui.stateView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	ui.stateView.
		SetBorder(true).
		SetTitle(" State ").
		SetBorderColor(tcell.ColorGreen)

	// Active filters view
	ui.filterView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	ui.filterView.
		SetBorder(true).
		SetTitle(" Active Filters ").
		SetBorderColor(tcell.ColorYellow)

	// Tab bar for switching between containers
	ui.tabBar = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	ui.tabBar.
		SetBorder(true).
		SetTitle(" Container Tabs ").
		SetBorderColor(tcell.ColorTeal)

	// Top info panel (status + state + filters)
	topPanel := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(ui.statusBar, 0, 2, false).
		AddItem(ui.stateView, 0, 1, false).
		AddItem(ui.filterView, 30, 1, false)

	// Create log pages for tabbed view
	ui.logPages = tview.NewPages()
	ui.shortcutsBar = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false).
		SetTextAlign(tview.AlignCenter)
	ui.shortcutsBar.
		SetBorder(true).
		SetTitle(" Shortcuts ").
		SetBorderColor(tcell.ColorDarkCyan)
	ui.shortcutsBar.SetText(shortcutsLegend)

	for i, container := range ui.containers {
		logView := tview.NewTextView().
			SetDynamicColors(true).
			SetScrollable(true).
			SetChangedFunc(func() {
				ui.app.Draw()
			})
		logView.
			SetBorder(true).
			SetTitle(fmt.Sprintf(" %s Logs ", container.Name)).
			SetBorderColor(tcell.ColorWhite)

		ui.logViews[container.Name] = &ContainerLogView{
			view:         logView,
			buffer:       make([]string, 0),
			latestStatus: "Waiting for logs...",
		}

		// Wrap log view in a flex container with footer hint
		logContainer := tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(logView, 0, 1, true)

			// Add each log view container as a page
		ui.logPages.AddPage(container.Name, logContainer, true, i == 0)
	}

	ui.currentTabIdx = 0

	ui.bottomPanel = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(ui.logPages, 0, 1, true).
		AddItem(ui.shortcutsBar, 0, 0, false)

	// Main layout
	ui.layout = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(topPanel, 8, 1, false).
		AddItem(ui.tabBar, 3, 1, false).
		AddItem(ui.bottomPanel, 0, 1, true)

	ui.app.SetRoot(ui.layout, true)

	// Set up keyboard handlers for tab switching
	ui.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Handle Ctrl+C to exit gracefully
		if event.Key() == tcell.KeyCtrlC {
			ui.app.Stop()
			return nil
		}
		switch event.Rune() {
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			tabIdx := int(event.Rune() - '1')
			if tabIdx < len(ui.containers) {
				ui.switchToTab(tabIdx)
				return nil
			}
		case '0':
			if len(ui.containers) == 10 {
				ui.switchToTab(9)
				return nil
			}
		case 'e':
			ui.exportCurrentLog()
			return nil
		case 'E':
			ui.exportAllLogs()
			return nil
		case 'h', 'H':
			ui.toggleShortcutsBar()
			return nil
		case 'c', 'C':
			if ui.currentTabIdx < len(ui.containers) {
				name := ui.containers[ui.currentTabIdx].Name
				go ui.ClearContainerLog(name)
			}
			return nil
		}
		return event
	})

	// Start update goroutine
	go ui.updateLoop()
}

func (ui *UI) updateLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		// All widget writes must happen on the tview goroutine via QueueUpdateDraw.
		// Batching them into a single call also minimises redraws under high log volume.
		ui.app.QueueUpdateDraw(func() {
			ui.updateStatusBar()
			ui.updateStateView()
			ui.updateFilterView()
			ui.updateTabBar()
			ui.flushPendingLogs()
		})
	}
}

// flushPendingLogs writes accumulated log lines for every container into their
// tview widgets. Must be called from within a QueueUpdateDraw callback.
func (ui *UI) flushPendingLogs() {
	for _, logView := range ui.logViews {
		logView.mu.Lock()
		if len(logView.pendingLines) == 0 && !logView.pendingNeedsRedraw {
			logView.mu.Unlock()
			continue
		}
		needsRedraw := logView.pendingNeedsRedraw
		var bufSnapshot []string
		if needsRedraw {
			bufSnapshot = make([]string, len(logView.buffer))
			copy(bufSnapshot, logView.buffer)
		}
		lines := logView.pendingLines
		logView.pendingLines = nil
		logView.pendingNeedsRedraw = false
		logView.mu.Unlock()

		if needsRedraw {
			logView.view.Clear()
			fmt.Fprintf(logView.view, "%s\n", strings.Join(bufSnapshot, "\n"))
			logView.view.ScrollToEnd()
		} else {
			fmt.Fprintf(logView.view, "%s\n", strings.Join(lines, "\n"))
		}
	}
}

func (ui *UI) switchToTab(idx int) {
	if idx < 0 || idx >= len(ui.containers) {
		return
	}
	ui.currentTabIdx = idx
	ui.logPages.SwitchToPage(ui.containers[idx].Name)
	ui.updateTabBar()
}

func (ui *UI) toggleShortcutsBar() {
	ui.showShortcuts = !ui.showShortcuts
	if ui.showShortcuts {
		ui.bottomPanel.ResizeItem(ui.shortcutsBar, 3, 0)
	} else {
		ui.bottomPanel.ResizeItem(ui.shortcutsBar, 0, 0)
	}
}

func (ui *UI) updateTabBar() {
	ui.tabBar.Clear()
	var tabs []string
	for i, container := range ui.containers {
		key := fmt.Sprintf("%d", (i+1)%10)
		name := container.Name
		// Shorten long names
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		if i == ui.currentTabIdx {
			tabs = append(tabs, fmt.Sprintf("[black:aqua] %s:%s [-:-]", key, name))
		} else {
			tabs = append(tabs, fmt.Sprintf("[aqua] %s[white]:%s", key, name))
		}
	}
	fmt.Fprintf(ui.tabBar, "  %s", strings.Join(tabs, "  "))
}

func (ui *UI) updateStatusBar() {
	ui.statusBar.Clear()

	services := ui.statusTracker.GetAllServices()

	// First pass: calculate max widths for alignment
	maxNameWidth := 0

	type serviceData struct {
		service     *status.ServiceStatus
		statusParts []string
	}
	var servicesData []serviceData

	// Iterate over containers in config order to maintain consistent display order
	for _, container := range ui.containers {
		service, exists := services[container.Name]
		if !exists {
			continue
		}

		service.Mu.RLock()

		if len(service.Name) > maxNameWidth {
			maxNameWidth = len(service.Name)
		}

		data := serviceData{
			service:     service,
			statusParts: []string{},
		}

		// Collect custom statuses
		for _, item := range service.CustomStatuses {
			var color string
			switch item.Status {
			case "ok":
				color = "green"
			case "pending":
				color = "yellow"
			case "error":
				color = "red"
			default:
				color = "gray"
			}

			statusText := item.Label
			if item.Value != "" {
				statusText += ": " + item.Value
			}
			data.statusParts = append(data.statusParts, fmt.Sprintf("[%s]%s[white]", color, statusText))
		}

		servicesData = append(servicesData, data)
		service.Mu.RUnlock()
	}

	// Second pass: format with alignment
	var lines []string
	for _, data := range servicesData {
		// Service name and running status
		runningIcon := "[red]●[white]"
		if data.service.Running {
			runningIcon = "[green]●[white]"
		}

		// Pad container name for alignment
		namePadding := maxNameWidth - len(data.service.Name)
		paddedName := data.service.Name + strings.Repeat(" ", namePadding)

		line := fmt.Sprintf("%s [yellow]%-*s[white]", runningIcon, maxNameWidth, paddedName)

		// Add custom statuses
		if len(data.statusParts) > 0 {
			line += " │ " + strings.Join(data.statusParts, " | ")
		}

		lines = append(lines, line)
	}

	fmt.Fprintf(ui.statusBar, "%s", strings.Join(lines, "\n"))
}

func (ui *UI) updateStateView() {
	if len(ui.containers) == 0 {
		return
	}
	containerName := ui.containers[ui.currentTabIdx].Name
	sm, ok := ui.stateManagers[containerName]
	if !ok || sm == nil {
		return
	}

	ui.stateView.Clear()
	currentState := sm.GetCurrentState()
	description := sm.GetStateDescription(currentState)

	var color string
	switch currentState {
	case "operational":
		color = "green"
	case "connecting", "initializing":
		color = "yellow"
	case "degraded":
		color = "orange"
	case "critical":
		color = "red"
	default:
		color = "white"
	}

	fmt.Fprintf(ui.stateView, "\n  [%s]%s[white]",
		color, description)
}

func (ui *UI) updateFilterView() {
	if len(ui.containers) == 0 {
		return
	}
	containerName := ui.containers[ui.currentTabIdx].Name
	fm, ok := ui.filterManagers[containerName]
	if !ok || fm == nil {
		return
	}

	ui.filterView.Clear()
	filters := fm.AllFilters()

	fmt.Fprintf(ui.filterView, "\n  Total: [yellow]%d[white] filters\n\n", len(filters))
	for i, f := range filters {
		if i >= 4 {
			fmt.Fprintf(ui.filterView, "  ... and %d more", len(filters)-4)
			break
		}
		fmt.Fprintf(ui.filterView, "  • %s\n", f)
	}
}

// AddLog adds a log entry to the UI
func (ui *UI) AddLog(entry docker.LogEntry, matchingFilters []string) {
	// Determine which container this log is from
	// Clean up container name (remove leading slash if present)
	containerName := strings.TrimPrefix(entry.ContainerName, "/")

	// Find the matching log view
	logView, exists := ui.logViews[containerName]
	if !exists {
		// If we don't have a view for this container, skip it
		return
	}

	logView.mu.Lock()
	defer logView.mu.Unlock()

	// Build filter tags
	filterTags := ""
	if len(matchingFilters) > 0 {
		tags := make([]string, len(matchingFilters))
		for i, f := range matchingFilters {
			tags[i] = fmt.Sprintf("[blue]%s[white]", f)
		}
		filterTags = " [" + strings.Join(tags, ",") + "]"
	}

	// Format timestamp
	timestamp := time.Now().Format("15:04:05")

	// Format log line (without container name since each view is for one container)
	logLine := fmt.Sprintf("[gray]%s[white]%s %s",
		timestamp, filterTags, strings.TrimRight(entry.Message, "\n"))

	// Add to buffer
	logView.buffer = append(logView.buffer, logLine)

	// Update latest status (strip color codes for cleaner display)
	cleanMessage := strings.TrimRight(entry.Message, "\n")
	if len(cleanMessage) > 60 {
		cleanMessage = cleanMessage[:57] + "..."
	}
	logView.latestStatus = cleanMessage

	// Check if we need to trim the buffer
	if len(logView.buffer) > ui.maxLogLines {
		logView.buffer = logView.buffer[len(logView.buffer)-ui.maxLogLines:]
		// Full redraw needed; discard any previously accumulated pending lines
		// since the full buffer write will cover them all
		logView.pendingLines = logView.pendingLines[:0]
		logView.pendingNeedsRedraw = true
	} else if !logView.pendingNeedsRedraw {
		// Normal case: accumulate for the next batch flush
		logView.pendingLines = append(logView.pendingLines, logLine)
	}
	// If pendingNeedsRedraw is already set the upcoming full redraw covers this line
}

// AddStatusMessage adds a status change message to all log views
func (ui *UI) AddStatusMessage(message string) {
	timestamp := time.Now().Format("15:04:05")
	logLine := fmt.Sprintf("[gray]%s[white] [green]► %s[white]", timestamp, message)

	for _, logView := range ui.logViews {
		logView.mu.Lock()
		logView.buffer = append(logView.buffer, logLine)
		if len(logView.buffer) > ui.maxLogLines {
			logView.buffer = logView.buffer[len(logView.buffer)-ui.maxLogLines:]
			logView.pendingLines = logView.pendingLines[:0]
			logView.pendingNeedsRedraw = true
		} else if !logView.pendingNeedsRedraw {
			logView.pendingLines = append(logView.pendingLines, logLine)
		}
		logView.mu.Unlock()
	}
}

// ClearContainerLog clears the log buffer and view for a specific container.
func (ui *UI) ClearContainerLog(containerName string) {
	containerName = strings.TrimPrefix(containerName, "/")

	logView, exists := ui.logViews[containerName]
	if !exists {
		return
	}

	logView.mu.Lock()
	logView.buffer = logView.buffer[:0]
	logView.pendingLines = logView.pendingLines[:0]
	logView.pendingNeedsRedraw = false
	logView.mu.Unlock()

	ui.app.QueueUpdateDraw(func() {
		logView.view.Clear()
	})
}

// AddStatusMessageToContainer adds a status message to a specific container's log view
func (ui *UI) AddStatusMessageToContainer(message string, containerName string) {
	timestamp := time.Now().Format("15:04:05")
	logLine := fmt.Sprintf("[gray]%s[white] [green]► %s[white]", timestamp, message)

	// Clean up container name (remove leading slash if present)
	containerName = strings.TrimPrefix(containerName, "/")

	logView, exists := ui.logViews[containerName]
	if !exists {
		return
	}

	logView.mu.Lock()
	defer logView.mu.Unlock()

	logView.buffer = append(logView.buffer, logLine)

	// Update latest status from status message
	if len(message) > 60 {
		logView.latestStatus = message[:57] + "..."
	} else {
		logView.latestStatus = message
	}

	if len(logView.buffer) > ui.maxLogLines {
		logView.buffer = logView.buffer[len(logView.buffer)-ui.maxLogLines:]
		logView.pendingLines = logView.pendingLines[:0]
		logView.pendingNeedsRedraw = true
	} else if !logView.pendingNeedsRedraw {
		logView.pendingLines = append(logView.pendingLines, logLine)
	}
}

var tvColorTagRe = regexp.MustCompile(`\[[a-zA-Z#,:\- ]*\]`)

// exportCurrentLog writes the buffered logs of the currently visible container
// to a file named <container-name>.<datetime>.log in the working directory.
func (ui *UI) exportCurrentLog() {
	if ui.currentTabIdx >= len(ui.containers) {
		return
	}
	containerName := ui.containers[ui.currentTabIdx].Name

	logView, exists := ui.logViews[containerName]
	if !exists {
		return
	}

	logView.mu.Lock()
	lines := make([]string, len(logView.buffer))
	copy(lines, logView.buffer)
	logView.mu.Unlock()

	datetime := time.Now().Format("2006-01-02T15-04-05")
	basename := fmt.Sprintf("%s.%s.log", containerName, datetime)

	filename := basename
	if ui.logsDir != "" {
		if err := os.MkdirAll(ui.logsDir, 0755); err == nil {
			filename = fmt.Sprintf("%s/%s", ui.logsDir, basename)
		}
	}

	f, err := os.Create(filename)
	if err != nil {
		return
	}
	defer f.Close()

	for _, line := range lines {
		clean := tvColorTagRe.ReplaceAllString(line, "")
		fmt.Fprintln(f, clean)
	}
}

// exportAllLogs writes the buffered logs of all tracked containers
// to files named <container-name>.<datetime>.log in the working directory.
func (ui *UI) exportAllLogs() {
	for _, container := range ui.containers {
		containerName := container.Name

		logView, exists := ui.logViews[containerName]
		if !exists {
			continue
		}

		logView.mu.Lock()
		lines := make([]string, len(logView.buffer))
		copy(lines, logView.buffer)
		logView.mu.Unlock()

		datetime := time.Now().Format("2006-01-02T15-04-05")
		basename := fmt.Sprintf("%s.%s.log", containerName, datetime)

		filename := basename
		if ui.logsDir != "" {
			if err := os.MkdirAll(ui.logsDir, 0755); err == nil {
				filename = fmt.Sprintf("%s/%s", ui.logsDir, basename)
			}
		}

		f, err := os.Create(filename)
		if err != nil {
			continue
		}

		for _, line := range lines {
			clean := tvColorTagRe.ReplaceAllString(line, "")
			fmt.Fprintln(f, clean)
		}
		f.Close()
	}
}

// Run starts the UI application
func (ui *UI) Run() error {
	return ui.app.Run()
}

// Stop stops the UI application
func (ui *UI) Stop() {
	ui.app.Stop()
}
