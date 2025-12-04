package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Logger struct {
	file   *os.File
	logger *log.Logger
	debug  bool
}

func New(path string) (*Logger, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	debugEnv := os.Getenv("CM_DEBUG")
	debug := debugEnv == "debug" || debugEnv == "trace"

	return &Logger{
		file:   file,
		logger: log.New(file, "", 0),
		debug:  debug,
	}, nil
}

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) log(level, msg string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.logger.Printf("[%s] %s: %s", timestamp, level, msg)
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/claude-manager.log"
	}
	return filepath.Join(home, ".claude-manager", "daemon.log")
}
