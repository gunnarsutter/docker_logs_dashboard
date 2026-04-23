package configbuilder

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LogLine represents a single line from an exported log file.
type LogLine struct {
	Raw     string // original line as stored in file
	Message string // cleaned message (timestamp stripped)
}

// ParseLogFile reads an exported log file and returns all non-empty lines.
// The exporter writes lines as: "HH:MM:SS [filters] message"
// We keep the full raw line but also expose just the message portion for pattern use.
func ParseLogFile(path string) ([]LogLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open log file: %w", err)
	}
	defer f.Close()

	var lines []LogLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := scanner.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}
		lines = append(lines, LogLine{
			Raw:     raw,
			Message: extractMessage(raw),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading log file: %w", err)
	}
	return lines, nil
}

// extractMessage strips the leading "HH:MM:SS " timestamp if present,
// returning the remainder of the line as the message.
func extractMessage(line string) string {
	// Format written by exporter: "15:04:05 <optional [filter]> message"
	// Timestamp is exactly 8 chars: "HH:MM:SS"
	if len(line) > 9 && line[2] == ':' && line[5] == ':' && line[8] == ' ' {
		return strings.TrimSpace(line[9:])
	}
	return line
}
