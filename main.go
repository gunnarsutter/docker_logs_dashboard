package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"docker-logs-dashboard/internal/config"
	"docker-logs-dashboard/internal/configbuilder"
	"docker-logs-dashboard/internal/dashboard"
	"docker-logs-dashboard/internal/docker"
	"docker-logs-dashboard/internal/ui"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	noConfig := flag.Bool("no-config", false, "Ignore config file and show container selector instead")
	api := flag.Bool("api", false, "Also start the config-builder API server")
	webPort := flag.String("web-port", "8080", "Port for the config-builder API server")
	configsDir := flag.String("configs-dir", "configs", "Directory to store saved configs (used by API server)")
	flag.Parse()

	// Track whether -config was explicitly provided by the user
	configExplicitlySet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicitlySet = true
		}
	})

	// Create Docker client
	dockerClient, err := docker.NewClient()
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}
	defer dockerClient.Close()

	// Load configuration
	var cfg *config.Config
	var configLoadErr error

	if *noConfig {
		// Skip config loading and go straight to selector
		configLoadErr = fmt.Errorf("container selector requested via -no-config flag")
	} else {
		resolvedConfig := *configPath
		if !filepath.IsAbs(resolvedConfig) && filepath.Dir(resolvedConfig) == "." {
			resolvedConfig = filepath.Join(*configsDir, resolvedConfig)
		}
		cfg, configLoadErr = config.Load(resolvedConfig)
	}

	// If -config was explicitly provided, a load failure is fatal
	if configLoadErr != nil && configExplicitlySet {
		log.Fatalf("Invalid config: %v", configLoadErr)
	}

	// If config loading failed (no explicit config), show container selector
	if configLoadErr != nil {
		fmt.Printf("Config file not found or invalid: %v\n", configLoadErr)
		fmt.Println("Showing container selector...")

		ctx := context.Background()
		containers, err := dockerClient.ListContainers(ctx)
		if err != nil {
			log.Fatalf("Failed to list containers: %v", err)
		}

		if len(containers) == 0 {
			log.Fatalf("No running Docker containers found")
		}

		// Create and run container selector
		selector := ui.NewContainerSelector(containers)
		selected, err := selector.Run()
		selector.Stop()

		if err != nil {
			log.Fatalf("Container selection failed: %v", err)
		}

		fmt.Printf("Selected %d container(s)\n", len(selected))

		// Create config from selected containers
		cfg = config.CreateConfigFromContainers(selected)
	}

	// Create dashboard
	dash := dashboard.New(cfg, dockerClient)

	// Optionally start config-builder API server in the background
	if *api {
		// Use ~/logs/ for the API server logs directory
		homeDir, err := os.UserHomeDir()
		logsDir := "logs"
		if err == nil {
			logsDir = filepath.Join(homeDir, "logs")
		}
		srv, err := configbuilder.NewServer("", logsDir, "generated_config.yaml", *configPath, *configsDir, *webPort)
		if err != nil {
			log.Fatalf("Failed to create config-builder server: %v", err)
		}
		go func() {
			fmt.Printf("Config Builder API running at http://localhost:%s\n", *webPort)
			if err := srv.Start(); err != nil {
				log.Printf("Config-builder API server stopped: %v", err)
			}
		}()
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		// Signal received, cancel context to stop log streaming
		cancel()
	}()

	// Start monitoring (blocks until UI exits)
	if err := dash.Start(ctx); err != nil {
		log.Fatalf("Dashboard error: %v", err)
	}
}
