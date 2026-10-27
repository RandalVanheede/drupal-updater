package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var logFile *os.File

// Init creates/opens the log file at ~/.config/drupal-updater/updater.log
// It's safe to call even if it fails - operations will just be no-ops.
func Init() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".config", "drupal-updater")
	os.MkdirAll(dir, 0755)
	
	path := filepath.Join(dir, "updater.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	logFile = f
}

// Close closes the log file
func Close() {
	if logFile != nil {
		logFile.Close()
	}
}

func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// Info logs an informational message
func Info(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logFile, "[%s] INFO  %s\n", timestamp(), msg)
}

// Error logs an error message
func Error(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logFile, "[%s] ERROR %s\n", timestamp(), msg)
}

// Warn logs a warning message
func Warn(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logFile, "[%s] WARN  %s\n", timestamp(), msg)
}
