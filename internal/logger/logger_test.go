package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInitWithFile(t *testing.T) {
	// Reset the logger for this test
	defaultLogger = nil
	once = *new(sync.Once)

	// Create temp directory
	tempDir := t.TempDir()

	// Initialize logger with file
	err := InitWithFile("debug", tempDir)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer Close()

	// Check log file was created
	logPath := GetLogFilePath()
	if logPath == "" {
		t.Fatal("Expected log file path, got empty string")
	}

	// Log some messages
	Debug("test debug message")
	Info("test info message")
	Warn("test warn message")
	Error("test error message")

	// Close to flush
	Close()

	// Read log file and verify no ANSI color codes
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	logContent := string(content)

	// Check messages are present
	if !strings.Contains(logContent, "test debug message") {
		t.Error("Debug message not found in log file")
	}
	if !strings.Contains(logContent, "test info message") {
		t.Error("Info message not found in log file")
	}

	// Check no ANSI color codes
	if strings.Contains(logContent, "\033[") {
		t.Error("Log file contains ANSI color codes")
	}

	// Check log file is in expected directory
	if filepath.Dir(logPath) != tempDir {
		t.Errorf("Log file not in expected directory: %s", logPath)
	}
}

func TestLogFilenameFormat(t *testing.T) {
	// Reset the logger for this test
	defaultLogger = nil
	once = *new(sync.Once)

	tempDir := t.TempDir()

	err := InitWithFile("info", tempDir)
	if err != nil {
		t.Fatalf("InitWithFile failed: %v", err)
	}
	defer Close()

	logPath := GetLogFilePath()
	filename := filepath.Base(logPath)

	// Check filename format: YYYY-MM-DD_HH-MM-SS_TZ.log
	if !strings.HasSuffix(filename, ".log") {
		t.Errorf("Log filename should end with .log: %s", filename)
	}

	// Should contain underscore separators
	parts := strings.Split(strings.TrimSuffix(filename, ".log"), "_")
	if len(parts) < 3 {
		t.Errorf("Log filename format incorrect: %s", filename)
	}
}
