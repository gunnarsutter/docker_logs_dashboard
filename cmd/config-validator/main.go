package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"docker-logs-dashboard/internal/config"
)

type containerSummary struct {
	Name         string `json:"name"`
	ContainerID  string `json:"container_id"`
	States       int    `json:"states"`
	Events       int    `json:"events"`
	Filters      int    `json:"filters"`
	StatusChecks int    `json:"status_checks"`
}

type validationResult struct {
	Valid         bool               `json:"valid"`
	Path          string             `json:"path"`
	Error         string             `json:"error,omitempty"`
	Containers    int                `json:"containers,omitempty"`
	GlobalStates  int                `json:"global_states,omitempty"`
	GlobalEvents  int                `json:"global_events,omitempty"`
	GlobalFilters int                `json:"global_filters,omitempty"`
	Entries       []containerSummary `json:"entries,omitempty"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	configsDir := flag.String("configs-dir", "configs", "Directory used to resolve relative config paths")
	showSummary := flag.Bool("summary", true, "Print a short summary for valid configs")
	outputFormat := flag.String("format", "text", "Output format: text or json")
	quiet := flag.Bool("quiet", false, "Shell-friendly mode: print nothing, use exit status only")
	flag.Parse()

	resolvedConfig := *configPath
	if !filepath.IsAbs(resolvedConfig) && filepath.Dir(resolvedConfig) == "." {
		resolvedConfig = filepath.Join(*configsDir, resolvedConfig)
	}

	if *outputFormat != "text" && *outputFormat != "json" {
		fmt.Fprintf(os.Stderr, "invalid format %q: expected text or json\n", *outputFormat)
		os.Exit(2)
	}
	if *quiet && *outputFormat == "json" {
		fmt.Fprintln(os.Stderr, "-quiet cannot be used with -format json")
		os.Exit(2)
	}

	cfg, err := config.Load(resolvedConfig)
	if err != nil {
		result := validationResult{
			Valid: false,
			Path:  resolvedConfig,
			Error: err.Error(),
		}
		if *outputFormat == "json" {
			writeJSON(result)
			os.Exit(1)
		}
		if *quiet {
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "INVALID: %s\n", resolvedConfig)
		fmt.Fprintf(os.Stderr, "Reason: %v\n", err)
		os.Exit(1)
	}

	result := validationResult{
		Valid:         true,
		Path:          resolvedConfig,
		Containers:    len(cfg.Containers),
		GlobalStates:  len(cfg.States),
		GlobalEvents:  len(cfg.Events),
		GlobalFilters: len(cfg.Filters),
		Entries:       make([]containerSummary, 0, len(cfg.Containers)),
	}
	for _, container := range cfg.Containers {
		result.Entries = append(result.Entries, containerSummary{
			Name:         container.Name,
			ContainerID:  container.ContainerID,
			States:       len(container.States),
			Events:       len(container.Events),
			Filters:      len(container.Filters),
			StatusChecks: len(container.StatusChecks),
		})
	}

	if *quiet {
		return
	}

	if *outputFormat == "json" {
		if !*showSummary {
			result.Entries = nil
		}
		writeJSON(result)
		return
	}

	fmt.Printf("VALID: %s\n", resolvedConfig)
	if !*showSummary {
		return
	}

	fmt.Printf("Containers: %d\n", len(cfg.Containers))
	fmt.Printf("Global states: %d\n", len(cfg.States))
	fmt.Printf("Global events: %d\n", len(cfg.Events))
	fmt.Printf("Global filters: %d\n", len(cfg.Filters))

	for _, container := range cfg.Containers {
		fmt.Printf("- %s -> %s", container.Name, container.ContainerID)
		if len(container.States) > 0 || len(container.Events) > 0 || len(container.Filters) > 0 || len(container.StatusChecks) > 0 {
			fmt.Printf(" (states=%d events=%d filters=%d status_checks=%d)", len(container.States), len(container.Events), len(container.Filters), len(container.StatusChecks))
		}
		fmt.Println()
	}
}

func writeJSON(result validationResult) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode JSON output: %v\n", err)
		os.Exit(2)
	}
}
