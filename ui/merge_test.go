package ui

import (
	"context"
	"testing"

	"drupal-updater/config"
	"drupal-updater/discovery"
)

func TestStartMergesOnlyRenamed(t *testing.T) {
	m := New(&config.Config{}, []discovery.Project{{Name: "a", Path: t.TempDir()}, {Name: "b", Path: t.TempDir()}}, "", "dev")
	m.confirmResults["a"] = "cleaned up"
	m.confirmResults["b"] = "rename failed: x"
	if cmd := m.startMerges([]string{"a", "b", "missing"}); cmd != nil || m.merging != 0 {
		t.Fatalf("started a merge for a project that was not renamed (merging=%d)", m.merging)
	}
}

func TestStartMergesRenamed(t *testing.T) {
	m := New(&config.Config{}, []discovery.Project{{Name: "a", Path: t.TempDir()}}, "", "dev")
	m.cancels = map[string]context.CancelFunc{"a": func() {}} // a finished run leaves its cancel behind
	m.confirmResults["a"] = "renamed to hotfix/x"
	m.branchNames["a"] = "hotfix/x"
	if cmd := m.startMerges([]string{"a"}); cmd == nil || m.merging != 1 {
		t.Fatalf("renamed project not started (merging=%d)", m.merging)
	}
	msg := (<-m.mergeChan).(mergeMsg) // not a git repo: fails fast with an error
	for !msg.done {
		msg = (<-m.mergeChan).(mergeMsg)
	}
	if msg.text[:len("✗")] != "✗" {
		t.Fatalf("expected failure, got %q", msg.text)
	}
}
