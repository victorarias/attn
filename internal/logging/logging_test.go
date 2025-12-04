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
	// Unset CM_DEBUG to ensure test isolation
	originalDebug := os.Getenv("CM_DEBUG")
	os.Unsetenv("CM_DEBUG")
	defer func() {
		if originalDebug != "" {
			os.Setenv("CM_DEBUG", originalDebug)
		}
	}()

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

func TestLogger_DebugEnabled(t *testing.T) {
	// Set CM_DEBUG for this test
	originalDebug := os.Getenv("CM_DEBUG")
	os.Setenv("CM_DEBUG", "debug")
	defer func() {
		if originalDebug != "" {
			os.Setenv("CM_DEBUG", originalDebug)
		} else {
			os.Unsetenv("CM_DEBUG")
		}
	}()

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	// Debug should be enabled now
	logger.Debug("debug message")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if !strings.Contains(string(content), "debug message") {
		t.Errorf("debug message should appear when CM_DEBUG=debug, got: %s", content)
	}
}

func TestLogger_Infof(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	logger.Infof("formatted %s %d", "message", 42)

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if !strings.Contains(string(content), "formatted message 42") {
		t.Errorf("log file should contain formatted message, got: %s", content)
	}
	if !strings.Contains(string(content), "INFO") {
		t.Errorf("log file should contain INFO level, got: %s", content)
	}
}

func TestLogger_Errorf(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	logger, err := New(logPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer logger.Close()

	logger.Errorf("error: %s (code %d)", "not found", 404)

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if !strings.Contains(string(content), "error: not found (code 404)") {
		t.Errorf("log file should contain formatted error, got: %s", content)
	}
	if !strings.Contains(string(content), "ERROR") {
		t.Errorf("log file should contain ERROR level, got: %s", content)
	}
}
