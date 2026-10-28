package executor

import "testing"

func TestDiffLocks(t *testing.T) {
	base := `{"packages":[{"name":"drupal/core","version":"10.3.1"},{"name":"drupal/gone","version":"1.0.0"},
		{"name":"drupal/same","version":"2.0.0"},{"name":"acme/dev","version":"dev-main","source":{"reference":"aaaaaaa111"}}],
		"packages-dev":[{"name":"drupal/devonly","version":"1.0.0"}]}`
	target := `{"packages":[{"name":"drupal/core","version":"10.3.2"},{"name":"drupal/new","version":"3.1.0"},
		{"name":"drupal/same","version":"2.0.0"},{"name":"acme/dev","version":"dev-main","source":{"reference":"bbbbbbb222"}}],
		"packages-dev":[{"name":"drupal/devonly","version":"2.0.0"}]}`
	want := "\n\n" +
		"| Production Changes | From             | To               |\n" +
		"| ------------------ | ---------------- | ---------------- |\n" +
		"| acme/dev           | dev-main aaaaaaa | dev-main bbbbbbb |\n" +
		"| drupal/core        | 10.3.1           | 10.3.2           |\n" +
		"| drupal/gone        | 1.0.0            | REMOVED          |\n" +
		"| drupal/new         | NEW              | 3.1.0            |\n"
	if got := diffLocks([]byte(base), []byte(target)); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := diffLocks([]byte(base), []byte(base)); got != "" {
		t.Errorf("unchanged lock: got %q", got)
	}
	if got := diffLocks([]byte("not json"), []byte(target)); got != "" {
		t.Errorf("bad base: got %q", got)
	}
}
