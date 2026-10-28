package executor

import (
	"strings"
	"testing"

	"drupal-updater/config"
)

func TestSafePermissionMode(t *testing.T) {
	for in, want := range map[string]string{"": "dontAsk", "acceptEdits": "acceptEdits", "bypassPermissions": "dontAsk", "auto": "dontAsk", "x": "dontAsk"} {
		if got := safePermissionMode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestUpdateModePrompt(t *testing.T) {
	if !strings.Contains(DefaultClaudePrompt, "{{updates}}") {
		t.Fatal("DefaultClaudePrompt lost its {{updates}} placeholder")
	}
	for mode, want := range map[string]string{"": standardUpdates, "disaster": disasterUpdates, "standard": standardUpdates} {
		configureClaude(&config.Config{UpdateMode: mode})
		if claudeCfg.updates != want {
			t.Errorf("mode %q: wrong update rules", mode)
		}
	}
}
