package coreupdates

import "testing"

const feedXML = `<project><releases>
<release><version>12.0.0-beta2</version><status>published</status></release>
<release><version>11.4.12</version><status>published</status><terms><term><name>Release type</name><value>Bug fixes</value></term></terms></release>
<release><version>11.4.10</version><status>published</status><terms><term><name>Release type</name><value>Security update</value></term></terms></release>
<release><version>11.4.9</version><status>published</status><terms><term><name>Release type</name><value>Insecure</value></term></terms></release>
<release><version>11.4.8</version><status>published</status></release>
<release><version>10.6.2</version><status>published</status></release>
<release><version>10.6.1</version><status>unpublished</status></release>
</releases></project>`

func TestCheck(t *testing.T) {
	rels, err := Parse([]byte(feedXML))
	if err != nil || len(rels) != 5 {
		t.Fatalf("parse: %v, %d releases", err, len(rels))
	}
	cases := []struct{ in, status, latest string }{
		{"11.4.12", UpToDate, "11.4.12"},
		{"v11.4.12", UpToDate, "11.4.12"},
		{"11.4.11", Behind, "11.4.12"},  // newer, but no security release after it
		{"11.4.8", Security, "11.4.12"}, // 11.4.10 is a security update
		{"11.4.9", Security, "11.4.12"}, // flagged insecure
		{"10.6.2", UpToDate, "10.6.2"},  // other majors do not count
	}
	for _, c := range cases {
		got, ok := Check(c.in, rels)
		if !ok || got.Status != c.status || got.Latest != c.latest {
			t.Errorf("%s: got %+v %v, want %s %s", c.in, got, ok, c.status, c.latest)
		}
	}
	// pre-release suffix is ignored: 11.5.0-beta1 counts as 11.5.0, ahead of everything published
	if got, ok := Check("11.5.0-beta1", rels); !ok || got.Status != UpToDate {
		t.Errorf("pre-release: got %+v %v", got, ok)
	}
	for _, bad := range []string{"dev-main", "11.x-dev", "", "9.5.11"} {
		if _, ok := Check(bad, rels); ok {
			t.Errorf("%q should be unknown", bad)
		}
	}
}
