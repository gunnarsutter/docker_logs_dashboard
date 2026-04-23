package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"docker-logs-dashboard/internal/configbuilder"
)

func main() {
	// Compute default logs directory
	defaultLogsDir := "logs"
	if homeDir, err := os.UserHomeDir(); err == nil {
		defaultLogsDir = filepath.Join(homeDir, "logs")
	}

	logFile := flag.String("log", "", "Path to a single exported log file (optional; use Logs tab to browse all)")
	logsDir := flag.String("logs-dir", defaultLogsDir, "Directory containing exported log files")
	outputFile := flag.String("output", "generated_config.yaml", "Default download filename")
	existingConfig := flag.String("existing", "", "Existing config file to read states from (optional)")
	configsDir := flag.String("configs-dir", "configs", "Directory to store saved config files")
	port := flag.String("port", "8080", "Port to listen on")
	flag.Parse()

	srv, err := configbuilder.NewServer(*logFile, *logsDir, *outputFile, *existingConfig, *configsDir, *port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("http://localhost:%s", *port)
	fmt.Printf("Config Builder API running at %s\n", url)
	fmt.Printf("Configs directory:         %s\n", *configsDir)
	fmt.Printf("Logs directory:            %s\n", *logsDir)
	fmt.Println("Press Ctrl+C to stop.")

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
