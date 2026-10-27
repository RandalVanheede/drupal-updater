package ui

import (
	"reflect"
	"testing"
)

func TestSplitList(t *testing.T) {
	if got := splitList(" /a, admin:/b,, "); !reflect.DeepEqual(got, []string{"/a", "admin:/b"}) {
		t.Fatalf("got %q", got)
	}
	if got := splitList(""); got != nil {
		t.Fatalf("empty input: got %q, want nil", got)
	}
}
