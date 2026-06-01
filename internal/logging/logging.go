package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
)

const (
	defaultMaxLogBytes         int64 = 20 << 20
	defaultRetainedLogBytes    int64 = 5 << 20
	defaultLogSizeCheckBytes   int64 = 256 << 10
	truncationMarkerTimeLayout       = "2006-01-02 15:04:05"
)

type Logger struct {
	file                *os.File
	logger              *log.Logger
	debug               bool
	maxBytes            int64
	retainedBytes       int64
	sizeCheckBytes      int64
	bytesSinceSizeCheck int64
	mu                  sync.Mutex
}

func New(path string) (*Logger, error) {
	return newWithLimits(path, defaultMaxLogBytes, defaultRetainedLogBytes, defaultLogSizeCheckBytes)
}

func newWithLimits(path string, maxBytes, retainedBytes, sizeCheckBytes int64) (*Logger, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	debug := config.DebugLevel() >= config.LogDebug

	logger := &Logger{
		file:           file,
		logger:         log.New(file, "", 0),
		debug:          debug,
		maxBytes:       maxBytes,
		retainedBytes:  retainedBytes,
		sizeCheckBytes: sizeCheckBytes,
	}
	logger.mu.Lock()
	err = logger.maybeTruncateLocked(true)
	logger.mu.Unlock()
	if err != nil {
		logger.Errorf("daemon.log truncation failed: %v", err)
	}
	return logger, nil
}

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.maybeTruncateLocked(false)
	timestamp := time.Now().Format(truncationMarkerTimeLayout)
	line := fmt.Sprintf("[%s] %s: %s", timestamp, level, msg)
	l.logger.Print(line)
	l.bytesSinceSizeCheck += int64(len(line) + 1)
}

func (l *Logger) Info(msg string) {
	l.log("INFO", msg)
}

func (l *Logger) Error(msg string) {
	l.log("ERROR", msg)
}

func (l *Logger) Debug(msg string) {
	if l.debug {
		l.log("DEBUG", msg)
	}
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...))
}

func DefaultLogPath() string {
	return config.LogPath()
}

func (l *Logger) maybeTruncateLocked(force bool) error {
	if l == nil || l.file == nil || l.maxBytes <= 0 || l.retainedBytes <= 0 {
		return nil
	}
	if !force && l.sizeCheckBytes > 0 && l.bytesSinceSizeCheck < l.sizeCheckBytes {
		return nil
	}
	l.bytesSinceSizeCheck = 0

	info, err := l.file.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size <= l.maxBytes {
		return nil
	}

	retainedBytes := l.retainedBytes
	if retainedBytes > l.maxBytes {
		retainedBytes = l.maxBytes
	}
	if retainedBytes > size {
		retainedBytes = size
	}

	tail := make([]byte, retainedBytes)
	start := size - retainedBytes
	if _, err := l.file.ReadAt(tail, start); err != nil && err != io.EOF {
		return err
	}
	if start > 0 {
		if newline := firstNewline(tail); newline >= 0 && newline+1 < len(tail) {
			tail = tail[newline+1:]
		}
	}

	marker := fmt.Sprintf(
		"[%s] INFO: daemon.log truncated; retained last %d bytes from previous %d-byte log\n",
		time.Now().Format(truncationMarkerTimeLayout),
		len(tail),
		size,
	)
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := l.file.WriteString(marker); err != nil {
		return err
	}
	if _, err := l.file.Write(tail); err != nil {
		return err
	}
	_, err = l.file.Seek(0, io.SeekEnd)
	return err
}

func firstNewline(data []byte) int {
	for i, b := range data {
		if b == '\n' {
			return i
		}
	}
	return -1
}
