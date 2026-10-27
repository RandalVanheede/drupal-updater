package executor

import "testing"

func TestSafePermissionMode(t *testing.T) {
	for in, want := range map[string]string{"": "dontAsk", "acceptEdits": "acceptEdits", "bypassPermissions": "dontAsk", "auto": "dontAsk", "x": "dontAsk"} {
		if got := safePermissionMode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
