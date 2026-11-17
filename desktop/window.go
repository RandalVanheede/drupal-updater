package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

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

// saveWindowSize runs from OnBeforeClose; it never blocks closing.
func saveWindowSize(ctx context.Context) bool {
	w, h := runtime.WindowGetSize(ctx)
	if b, err := json.Marshal(windowSize{w, h}); err == nil {
		_ = os.MkdirAll(filepath.Dir(windowFile()), 0o755)
		_ = os.WriteFile(windowFile(), b, 0o644)
	}
	return false
}
