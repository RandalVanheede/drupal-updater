package coreupdates

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestTrackerStatusesCachesAndBacksOff(t *testing.T) {
	var calls atomic.Int32
	fail := true
	tr := &Tracker{FetchReleases: func(context.Context) ([]Release, error) {
		calls.Add(1)
		if fail {
			return nil, errors.New("offline")
		}
		return []Release{{Version: "11.4.12"}}, nil
	}}
	if got := tr.Statuses([]string{"11.4.11"}); len(got) != 0 {
		t.Fatalf("offline should give no statuses, got %v", got)
	}
	tr.Statuses([]string{"11.4.11"}) // within the retry pause: no second download
	if calls.Load() != 1 {
		t.Fatalf("failed download retried at once: %d calls", calls.Load())
	}
	fail = false
	tr.ResetReleases() // what the Refresh button does
	if got := tr.Statuses([]string{"11.4.11"}); got["11.4.11"].Status != Behind {
		t.Fatalf("got %v", got)
	}
	tr.Statuses([]string{"11.4.12"})
	if calls.Load() != 2 {
		t.Fatalf("cache not reused: %d calls", calls.Load())
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func lock(version string) []byte {
	return []byte(`{"packages":[{"name":"drupal/core","version":"` + version + `"}]}`)
}

func TestTrackerRefreshReadsOrigin(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "t"}, {"GIT_AUTHOR_EMAIL", "t@e.x"}, {"GIT_COMMITTER_NAME", "t"}, {"GIT_COMMITTER_EMAIL", "t@e.x"}} {
		t.Setenv(kv[0], kv[1])
	}
	root := t.TempDir()
	origin, a, b := filepath.Join(root, "o.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "clone", "-q", origin, a)
	os.WriteFile(filepath.Join(a, "composer.lock"), lock("11.4.1"), 0o644)
	git(t, a, "add", ".")
	git(t, a, "commit", "-qm", "1")
	git(t, a, "push", "-q", "origin", "HEAD:main")
	git(t, root, "clone", "-q", origin, b) // stale once a pushes again
	os.WriteFile(filepath.Join(a, "composer.lock"), lock("11.4.9"), 0o644)
	git(t, a, "commit", "-qam", "2")
	git(t, a, "push", "-q", "origin", "HEAD:main")

	var tr Tracker
	if r := tr.Refresh(b, "main", false, true); r.Version != "11.4.1" || r.FetchFailed { // skipFetch: what is on disk
		t.Fatalf("no fetch: %+v", r)
	}
	if r := tr.Refresh(b, "main", false, false); r.Version != "11.4.9" || r.FetchFailed { // never fetched: fetches
		t.Fatalf("auto fetch: %+v", r)
	}
	// a broken origin: the fetch fails, is reported, and is not retried automatically
	git(t, b, "remote", "set-url", "origin", filepath.Join(root, "missing.git"))
	if r := tr.Refresh(b, "main", true, false); !r.FetchFailed || r.Version != "11.4.9" {
		t.Fatalf("forced fetch of a broken origin: %+v", r)
	}
	if r := tr.Refresh(b, "main", false, false); !r.FetchFailed {
		t.Fatalf("a failed fetch should not be retried or forgotten: %+v", r)
	}
}

// the fetch only accepts https/ssh remotes in production; the tests use local repositories
func TestMain(m *testing.M) {
	os.Setenv("DU_ALLOW_LOCAL_REMOTES", "1")
	os.Exit(m.Run())
}
