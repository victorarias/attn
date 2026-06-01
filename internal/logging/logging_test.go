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
	// Unset DEBUG to ensure test isolation
	originalDebug := os.Getenv("DEBUG")
	os.Unsetenv("DEBUG")
	defer func() {
		if originalDebug != "" {
			os.Setenv("DEBUG", originalDebug)
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
	// Set DEBUG for this test
	originalDebug := os.Getenv("DEBUG")
	os.Setenv("DEBUG", "debug")
	defer func() {
		if originalDebug != "" {
			os.Setenv("DEBUG", originalDebug)
		} else {
			os.Unsetenv("DEBUG")
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
		t.Errorf("debug message should appear when DEBUG=debug, got: %s", content)
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

func TestLogger_TruncatesOversizedLogOnStartup(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")
	original := strings.Repeat("old line should be removed\n", 20) + "keep line 1\nkeep line 2\n"
	if err := os.WriteFile(logPath, []byte(original), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	logger, err := newWithLimits(logPath, 120, 40, 16)
	if err != nil {
		t.Fatalf("newWithLimits() error: %v", err)
	}
	defer logger.Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "daemon.log truncated") {
		t.Fatalf("expected truncation marker, got: %s", text)
	}
	if !strings.Contains(text, "keep line 1\nkeep line 2\n") {
		t.Fatalf("expected retained tail, got: %s", text)
	}
	if strings.Contains(text, "old line should be removed") {
		t.Fatalf("expected old prefix to be removed, got: %s", text)
	}
}

func TestLogger_TruncatesOversizedLogAfterWrites(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	logger, err := newWithLimits(logPath, 180, 80, 1)
	if err != nil {
		t.Fatalf("newWithLimits() error: %v", err)
	}
	defer logger.Close()

	for i := 0; i < 10; i++ {
		logger.Infof("line %02d %s", i, strings.Repeat("x", 20))
	}
	logger.Info("tail survives")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "daemon.log truncated") {
		t.Fatalf("expected truncation marker, got: %s", text)
	}
	if !strings.Contains(text, "tail survives") {
		t.Fatalf("expected writes after truncation to continue, got: %s", text)
	}
	if strings.Contains(text, "line 00") {
		t.Fatalf("expected earliest log lines to be removed, got: %s", text)
	}
}

func TestLogger_TruncatesAfterSingleLargeWrite(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	logger, err := newWithLimits(logPath, 100, 40, 1000)
	if err != nil {
		t.Fatalf("newWithLimits() error: %v", err)
	}
	defer logger.Close()

	logger.Info("large " + strings.Repeat("x", 500) + " tail")

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if info.Size() > 100 {
		t.Fatalf("expected large write to be truncated immediately, size=%d", info.Size())
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "daemon.log truncated") {
		t.Fatalf("expected truncation marker, got: %s", text)
	}
	if !strings.Contains(text, "tail") {
		t.Fatalf("expected retained tail of large write, got: %s", text)
	}
}
