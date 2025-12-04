package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogger_WritesToFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	logger.Info("test message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if !strings.Contains(string(content), "test message") {
		t.Errorf("log file should contain 'test message', got: %s", content)
	}
}

func TestLogger_RespectsDebugLevel(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	// Debug disabled by default
	logger.Debug("debug message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if strings.Contains(string(content), "debug message") {
		t.Errorf("debug message should not appear when debug disabled")
	}
}
