package coreupdates

import (
	"context"
	"sync"
	"time"

	"drupal-updater/discovery"
	"drupal-updater/executor"
	"drupal-updater/logging"
)

const (
	releaseTTL   = time.Hour      // how long the downloaded release history is reused
	releaseRetry = time.Minute    // pause after a failed download
	fetchEvery   = 24 * time.Hour // automatic git fetch per project
)

// Refresh is the outcome of Tracker.Refresh for one project.
type Refresh struct {
	Version     string `json:"version"`
	FetchFailed bool   `json:"fetchFailed"` // origin could not be fetched (offline, no key): the version may be old
}

// Tracker is what the desktop app and the TUI share: the cached Drupal release history, and the per-project
// "installed core version on origin" lookup with its fetch rules. The zero value is ready to use.
type Tracker struct {
	FetchReleases func(context.Context) ([]Release, error) // defaults to Fetch; replaced in tests

	relMu      sync.Mutex // one release download at a time; concurrent callers reuse its result
	mu         sync.Mutex
	releases   []Release
	relFetched time.Time
	relFailed  time.Time
	failed     map[string]bool // projects whose origin fetch failed this session (not retried automatically)
}

// Statuses compares installed versions with Drupal's published releases. Offline (or when the download
// failed) the map is empty.
func (t *Tracker) Statuses(versions []string) map[string]Result {
	t.relMu.Lock()
	t.mu.Lock()
	stale := (t.releases == nil || time.Since(t.relFetched) > releaseTTL) && time.Since(t.relFailed) > releaseRetry
	t.mu.Unlock()
	if stale {
		fetch := t.FetchReleases
		if fetch == nil {
			fetch = Fetch
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rels, err := fetch(ctx)
		cancel()
		t.mu.Lock()
		if err != nil {
			logging.Info("core release history: %v", err)
			t.relFailed = time.Now()
		} else {
			t.releases, t.relFetched = rels, time.Now()
		}
		t.mu.Unlock()
	}
	t.relMu.Unlock()

	t.mu.Lock()
	rels := t.releases
	t.mu.Unlock()
	out := map[string]Result{}
	for _, v := range versions {
		if r, ok := Check(v, rels); ok {
			out[v] = r
		}
	}
	return out
}

// ResetReleases drops the cached release history so the next Statuses call downloads it again.
func (t *Tracker) ResetReleases() {
	t.mu.Lock()
	t.releases, t.relFailed = nil, time.Time{}
	t.mu.Unlock()
}

// Refresh returns the Drupal core version committed on origin's start branch (the working tree can be far
// behind), falling back to the working-tree composer.lock. It fetches origin first when the last fetch is
// over a day old, or always when force is set; skipFetch (a project that is being updated) never fetches.
// A failed fetch is not retried automatically until the process restarts.
func (t *Tracker) Refresh(path, startBranch string, force, skipFetch bool) Refresh {
	t.mu.Lock()
	failed := t.failed[path]
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !skipFetch && (force || (!failed && !executor.FetchedWithin(path, fetchEvery))) {
		err := executor.FetchOrigin(ctx, path)
		failed = err != nil
		if err != nil {
			logging.Info("fetch %s: %v", path, err)
		}
		t.mu.Lock()
		if t.failed == nil {
			t.failed = map[string]bool{}
		}
		t.failed[path] = failed
		t.mu.Unlock()
	}
	if data, err := executor.RemoteStartFile(ctx, path, startBranch, "composer.lock"); err == nil {
		if v := discovery.CoreVersionFromLock(data); v != "" {
			return Refresh{v, failed}
		}
	}
	return Refresh{discovery.CoreVersion(path), failed}
}
