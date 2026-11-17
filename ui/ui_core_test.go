package ui

import (
	"strings"
	"testing"

	"drupal-updater/coreupdates"
)

func TestCoreMarker(t *testing.T) {
	m := &model{
		coreVer: map[string]string{"a": "11.4.12", "b": "11.4.8", "c": "11.4.11", "d": "dev-main"},
		coreStatus: map[string]coreupdates.Result{
			"11.4.12": {Status: coreupdates.UpToDate},
			"11.4.8":  {Status: coreupdates.Security},
			"11.4.11": {Status: coreupdates.Behind},
		},
	}
	for name, want := range map[string][2]string{"a": {"●", "11.4.12"}, "b": {"◆", "11.4.8"}, "c": {"■", "11.4.11"}, "d": {"·", "dev-main"}} {
		got := m.coreMarker(name)
		if !strings.Contains(got, want[0]) || !strings.Contains(got, want[1]) {
			t.Errorf("%s: %q should contain %q and %q", name, got, want[0], want[1])
		}
	}
	if m.coreMarker("unknown") != "" {
		t.Error("a project without a version shows nothing")
	}
}
