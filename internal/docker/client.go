package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Client wraps the Docker client
type Client struct {
	cli    *client.Client
	logger *log.Logger
}

// LogEntry represents a single log line from a container
type LogEntry struct {
	ContainerName string
	ContainerID   string
	Message       string
	Timestamp     string
}

// NewClient creates a new Docker client
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Create log file for debugging
	logFile, err := os.OpenFile("docker-client.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	logger := log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)

	return &Client{cli: cli, logger: logger}, nil
}

// Close closes the Docker client connection
func (c *Client) Close() error {
	return c.cli.Close()
}

// StreamLogs streams logs from a container
func (c *Client) StreamLogs(ctx context.Context, containerID string, showTail bool, logChan chan<- LogEntry) error {
	c.logger.Printf("[%s] StreamLogs called - showTail=%v", containerID[:12], showTail)

	// First, verify the container exists
	containerInfo, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		c.logger.Printf("[%s] Failed to inspect container: %v", containerID[:12], err)
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
	}

	// For initial connection, show last 50 lines
	// For reconnections, use Since to only get new logs from now forward
	if showTail {
		options.Tail = "50"
		c.logger.Printf("[%s] Using Tail=50 for initial load", containerID[:12])
	} else {
		// Use Since with current time to only get future logs
		// This prevents Docker from sending all historical logs
		options.Since = time.Now().Add(-1 * time.Second).Format(time.RFC3339Nano)
		c.logger.Printf("[%s] Using Since=%s to stream from now forward", containerID[:12], options.Since)
	}

	logs, err := c.cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		c.logger.Printf("[%s] Failed to get logs: %v", containerID[:12], err)
		return fmt.Errorf("failed to get logs for container %s: %w", containerID, err)
	}
	c.logger.Printf("[%s] Log stream established", containerID[:12])

	// Block and stream logs (this function will return when stream ends or context cancelled)
	defer logs.Close()
	defer c.logger.Printf("[%s] Log stream function exiting", containerID[:12])
	reader := bufio.NewReader(logs)
	lineCount := 0
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			c.logger.Printf("[%s] Context cancelled after %d lines in %v", containerID[:12], lineCount, time.Since(startTime))
			return nil
		default:
			// Docker log format includes 8-byte header, so we need to skip it
			// Read header (8 bytes)
			header := make([]byte, 8)
			_, err := io.ReadFull(reader, header)
			if err != nil {
				// Suppress errors during shutdown (context canceled)
				if err != io.EOF && ctx.Err() == nil {
					c.logger.Printf("[%s] Error reading log header after %d lines: %v", containerID[:12], lineCount, err)
				}
				return nil
			}

			// Header format: [stream_type, 0, 0, 0, size1, size2, size3, size4]
			// Extract message size from last 4 bytes
			size := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])

			// Read the actual log message
			message := make([]byte, size)
			_, err = io.ReadFull(reader, message)
			if err != nil {
				// Suppress errors during shutdown (context canceled)
				if err != io.EOF && ctx.Err() == nil {
					c.logger.Printf("[%s] Error reading log message after %d lines: %v", containerID[:12], lineCount, err)
				}
				return nil
			}

			lineCount++
			if lineCount == 1 {
				c.logger.Printf("[%s] First log line received", containerID[:12])
			}

			entry := LogEntry{
				ContainerName: containerInfo.Name,
				ContainerID:   containerID,
				Message:       string(message),
			}

			select {
			case logChan <- entry:
			case <-ctx.Done():
				c.logger.Printf("[%s] Context cancelled while sending log entry", containerID[:12])
				return nil
			}
		}
	}
}

// ListContainers returns a list of running containers
func (c *Client) ListContainers(ctx context.Context) ([]types.Container, error) {
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	return containers, nil
}

// ContainerExists checks if a container exists
func (c *Client) ContainerExists(ctx context.Context, containerID string) (bool, error) {
	_, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsContainerRunning checks if a container exists and is running
func (c *Client) IsContainerRunning(ctx context.Context, containerID string) (bool, error) {
	containerInfo, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return containerInfo.State.Running, nil
}

// GetContainerIDByName finds a running container by name and returns its ID
// Returns empty string if not found
func (c *Client) GetContainerIDByName(ctx context.Context, containerName string) (string, error) {
	containers, err := c.ListContainers(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	for _, cont := range containers {
		// Container names in Docker include a leading slash, so normalize for comparison
		for _, name := range cont.Names {
			if name == "/"+containerName || name == containerName {
				return cont.ID, nil
			}
		}
	}
	return "", nil
}

// IsContainerRunningByName checks if a container with the given name is running
// This handles container recreation - it finds the current container by name
func (c *Client) IsContainerRunningByName(ctx context.Context, containerName string) (bool, error) {
	containerID, err := c.GetContainerIDByName(ctx, containerName)
	if err != nil {
		return false, err
	}
	if containerID == "" {
		// Container not found
		return false, nil
	}
	return c.IsContainerRunning(ctx, containerID)
}
