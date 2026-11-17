package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"drupal-updater/config"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const defaultW, defaultH = 900, 600

type windowSize struct{ Width, Height int }

func windowFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "drupal-updater", "window.json")
}

// loadWindowSize returns the last saved size, or the defaults when missing or implausible.
func loadWindowSize() (int, int) {
	var s windowSize
	if b, err := os.ReadFile(windowFile()); err != nil || json.Unmarshal(b, &s) != nil ||
		s.Width < 400 || s.Height < 300 || s.Width > 10000 || s.Height > 10000 {
		return defaultW, defaultH
	}
	return s.Width, s.Height
}

// saveWindowSize stores the size on close; failures are ignored (it is only a convenience).
func saveWindowSize(ctx context.Context) {
	w, h := runtime.WindowGetSize(ctx)
	if b, err := json.Marshal(windowSize{w, h}); err == nil {
		_ = os.MkdirAll(filepath.Dir(windowFile()), 0o755)
		_ = config.WriteFileAtomic(windowFile(), b, 0o644)
	}
}
