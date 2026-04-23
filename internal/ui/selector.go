package ui

import (
	"fmt"
	"sort"

	"github.com/docker/docker/api/types"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ContainerSelector provides a TUI for selecting Docker containers
type ContainerSelector struct {
	app        *tview.Application
	list       *tview.List
	selected   map[string]bool
	containers []types.Container
}

// NewContainerSelector creates a new container selector
func NewContainerSelector(containers []types.Container) *ContainerSelector {
	// Sort containers by name for consistent display
	sorted := make([]types.Container, len(containers))
	copy(sorted, containers)
	sort.Slice(sorted, func(i, j int) bool {
		ni := sorted[i].Names[0]
		nj := sorted[j].Names[0]
		if ni[0] == '/' {
			ni = ni[1:]
		}
		if nj[0] == '/' {
			nj = nj[1:]
		}
		return ni < nj
	})

	return &ContainerSelector{
		app:        tview.NewApplication(),
		list:       tview.NewList(),
		selected:   make(map[string]bool),
		containers: sorted,
	}
}

// Run starts the container selector UI and returns selected container info
func (cs *ContainerSelector) Run() ([]struct{ ID, Name string }, error) {
	cs.list.ShowSecondaryText(true)

	// Track confirmation state
	confirmed := false

	// Build the list without callbacks (we'll handle input differently)
	for idx := range cs.containers {
		c := cs.containers[idx] // capture by value
		name := c.Names[0]
		if name[0] == '/' {
			name = name[1:]
		}

		state := c.State
		status := fmt.Sprintf("[%s] %s", state, c.Status)

		// Add item without a select callback - we'll handle selection via input capture
		cs.list.AddItem(name, status, ' ', nil)
	}

	cs.refreshDisplay()

	// Build main layout
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[yellow]Select containers to monitor[white]\n" +
			"[teal]Space to toggle selection, Enter to confirm, Esc to cancel")
	header.SetBorder(true).SetTitle(" Container Selector ").SetBorderColor(tcell.ColorTeal)

	// Footer showing selection count
	footer := tview.NewTextView().
		SetDynamicColors(true)
	footer.SetBorder(true).SetTitle(" Selection ").SetBorderColor(tcell.ColorGreen)

	// Update footer
	updateFooter := func() {
		count := len(cs.selected)
		if count == 0 {
			footer.SetText("[gray]No containers selected - select at least one and press Enter")
		} else {
			footer.SetText(fmt.Sprintf("[green]%d container(s) selected - Press Enter to confirm", count))
		}
	}

	updateFooter()

	// Main layout
	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(header, 4, 1, false).
		AddItem(cs.list, 0, 1, true).
		AddItem(footer, 2, 1, false)

	cs.app.SetRoot(layout, true)

	// Set input capture BEFORE running the app
	cs.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			// Confirm selection only if at least one container is selected
			if len(cs.selected) > 0 {
				confirmed = true
				cs.app.Stop()
				return nil
			}
			// If no selection, ignore Enter
			return nil

		case tcell.KeyEscape:
			// Cancel selection
			cs.selected = make(map[string]bool)
			confirmed = false
			cs.app.Stop()
			return nil

		case tcell.KeyUp, tcell.KeyDown:
			// Let the list handle arrow keys
			return event

		default:
			// Check for space key by rune to toggle selection
			if event.Rune() == ' ' {
				currentIdx := cs.list.GetCurrentItem()
				cs.toggleContainer(currentIdx)
				// Update footer text
				count := len(cs.selected)
				if count == 0 {
					footer.SetText("[gray]No containers selected - select at least one and press Enter")
				} else {
					footer.SetText(fmt.Sprintf("[green]%d container(s) selected - Press Enter to confirm", count))
				}
				return nil
			}
			return event
		}
	})

	// Run the UI (blocks until app.Stop() is called)
	if err := cs.app.Run(); err != nil {
		return nil, fmt.Errorf("UI error: %w", err)
	}

	// Check if user confirmed selection
	if !confirmed || len(cs.selected) == 0 {
		return nil, fmt.Errorf("no containers selected")
	}

	// Build result from selected containers
	var result []struct{ ID, Name string }
	for _, c := range cs.containers {
		if cs.selected[c.ID] {
			name := c.Names[0]
			if name[0] == '/' {
				name = name[1:]
			}
			result = append(result, struct{ ID, Name string }{ID: c.ID, Name: name})
		}
	}

	return result, nil
}

// Stop stops the UI
func (cs *ContainerSelector) Stop() {
	cs.app.Stop()
}

// toggleContainer toggles the selection of a container by index
func (cs *ContainerSelector) toggleContainer(idx int) {
	if idx < 0 || idx >= len(cs.containers) {
		return
	}
	containerID := cs.containers[idx].ID
	cs.selected[containerID] = !cs.selected[containerID]
	cs.updateItemDisplay(idx)
}

// updateItemDisplay updates a single item's display text to show selection status
func (cs *ContainerSelector) updateItemDisplay(idx int) {
	if idx < 0 || idx >= len(cs.containers) {
		return
	}

	name := cs.containers[idx].Names[0]
	if name[0] == '/' {
		name = name[1:]
	}

	// Add checkmark if selected
	if cs.selected[cs.containers[idx].ID] {
		name = "[green]✓[white] " + name
	} else {
		name = "  " + name
	}

	state := cs.containers[idx].State
	status := fmt.Sprintf("[%s] %s", state, cs.containers[idx].Status)

	// Update the item text directly without removing/adding
	cs.list.SetItemText(idx, name, status)
}

// refreshDisplay updates all list item displays to show selection status
func (cs *ContainerSelector) refreshDisplay() {
	itemCount := cs.list.GetItemCount()
	for i := 0; i < itemCount; i++ {
		cs.updateItemDisplay(i)
	}
}
