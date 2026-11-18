package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"drupal-updater/config"
)

// gitCmd is git with hooks and fsmonitor off, like git(), but cancellable and with separate stdout.
func gitCmd(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// remoteURL is what an origin URL may look like: https, ssh, or scp-style [user@]host:path (host may be an alias
// from ~/.ssh/config). Not a local path, file://,
// git:// or a remote helper (ext::, fd::), and never something that starts like an option.
var remoteURL = regexp.MustCompile(`^(https://[A-Za-z0-9][^\s/]*/\S*|ssh://[A-Za-z0-9][^\s/]*/\S*|[A-Za-z0-9._-]+@[A-Za-z0-9][A-Za-z0-9.-]*:\S+|[A-Za-z0-9][A-Za-z0-9.-]*:[^/\s:]\S*)$`)

// allowLocalRemotes lets the tests fetch from local paths; it is read from the environment of the app, which a
// container cannot set.
func allowLocalRemotes() bool { return os.Getenv("DU_ALLOW_LOCAL_REMOTES") == "1" }

func remoteAllowed(u string) bool {
	if remoteURL.MatchString(u) {
		return true
	}
	return allowLocalRemotes() && (filepath.IsAbs(u) || strings.HasPrefix(u, "file:///")) && !strings.Contains(u, "::")
}

// gitPath asks git where one of the repository's paths lives (worktrees and .git files included).
func gitPath(ctx context.Context, dir string, env []string, name string) (string, error) {
	out, err := gitCmd(ctx, dir, env, "rev-parse", "--git-path", name).Output()
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p, nil
}

var (
	sha1or256 = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	trackRef  = regexp.MustCompile(`^refs/remotes/origin/[A-Za-z0-9._/-]+$`)
)

// originRefs lists refs/remotes/origin/* of the repository at env as "update <ref> <sha>" lines for update-ref,
// skipping anything that does not look like a plain branch name and object id.
func originRefs(ctx context.Context, dir string, env []string) (string, error) {
	out, err := gitCmd(ctx, dir, env, "for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/origin/").Output()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if ok && sha1or256.MatchString(sha) && trackRef.MatchString(ref) && !strings.Contains(ref, "..") && ref != "refs/remotes/origin/HEAD" {
			b.WriteString("update " + ref + " " + sha + "\n")
		}
	}
	return b.String(), nil
}

func updateRefs(ctx context.Context, dir string, env []string, lines string) error {
	if lines == "" {
		return nil
	}
	cmd := gitCmd(ctx, dir, env, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(lines)
	return cmd.Run()
}

// FetchOrigin refreshes origin's remote-tracking branches (no tags, no submodules). It never waits on a prompt
// (no terminal, ssh BatchMode unless the user configured ssh) and stops when ctx ends.
//
// A project's .git/config can be written from the ddev containers, and git has too many settings that run programs
// or steer the network (proxies, helpers, cookie files, upload-pack, TLS checks) to pin them one by one. So the fetch
// does not read it at all: it runs in a scratch repository that only has the user's own (system and global) git
// configuration, with the project's object store as its object directory, and the resulting branch positions are
// copied into the project with update-ref (no network, hooks off). Only the origin URL comes from the project, and
// it has to look like an https, ssh or scp-style remote.
func FetchOrigin(ctx context.Context, dir string) error {
	base := EnrichedEnv()
	out, err := gitCmd(ctx, dir, base, "remote", "get-url", "origin").Output()
	if err != nil {
		return err
	}
	url := strings.TrimSpace(string(out))
	if !remoteAllowed(url) {
		return fmt.Errorf("refusing to fetch from %q: only https, ssh and scp-style remotes", url)
	}
	objects, err := gitPath(ctx, dir, base, "objects")
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "drupal-updater-fetch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	if err := gitCmd(ctx, scratch, base, "init", "--bare", "-q").Run(); err != nil {
		return err
	}

	env := append(append([]string{}, base...), "GIT_DIR="+scratch, "GIT_OBJECT_DIRECTORY="+objects)
	protocols := "https:ssh"
	if allowLocalRemotes() {
		protocols += ":file"
	}
	if os.Getenv("GIT_ALLOW_PROTOCOL") == "" {
		env = append(env, "GIT_ALLOW_PROTOCOL="+protocols)
	}
	// the user's own ssh command (system, global or included config) is honoured; BatchMode only fills the gap
	if user, err := gitCmd(ctx, scratch, base, "config", "--get", "core.sshCommand").Output(); (err != nil || strings.TrimSpace(string(user)) == "") && os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
	}
	// a shallow clone keeps its boundary: without it the scratch repository cannot parse the missing parents
	if p, err := gitPath(ctx, dir, base, "shallow"); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			var keep []string
			for _, l := range strings.Split(string(data), "\n") {
				if l = strings.TrimSpace(l); sha1or256.MatchString(l) {
					keep = append(keep, l)
				}
			}
			if len(keep) > 0 {
				_ = os.WriteFile(filepath.Join(scratch, "shallow"), []byte(strings.Join(keep, "\n")+"\n"), 0o600)
			}
		}
	}

	// the scratch repository knows what the project already has, so only new objects are transferred
	have, err := originRefs(ctx, dir, base)
	if err != nil {
		return err
	}
	if err := updateRefs(ctx, scratch, env, have); err != nil {
		return err
	}
	fetch := gitCmd(ctx, scratch, env, "-c", "http.lowSpeedLimit=1000", "-c", "http.lowSpeedTime=15",
		"fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--no-auto-maintenance", "--no-write-fetch-head",
		"--", url, "+refs/heads/*:refs/remotes/origin/*")
	if err := fetch.Run(); err != nil {
		return err
	}
	got, err := originRefs(ctx, scratch, env)
	if err != nil {
		return err
	}
	if err := updateRefs(ctx, dir, base, got); err != nil {
		return err
	}
	// FetchedWithin goes by FETCH_HEAD's age; written through a rename so a symlink planted there is replaced, not followed
	if p, err := gitPath(ctx, dir, base, "FETCH_HEAD"); err == nil {
		_ = config.WriteFileAtomic(p, []byte(time.Now().Format(time.RFC3339)+"\tdrupal-updater fetch\n"), 0o644)
	}
	return nil
}

// RemoteStartFile returns file as committed on origin's start branch: the first name of the "|"-separated
// configured list that exists as origin/<name>, else origin/HEAD. It reads the remote-tracking ref as it is
// (see FetchOrigin) and never touches the working tree.
func RemoteStartFile(ctx context.Context, dir, configured, file string) ([]byte, error) {
	env := EnrichedEnv()
	refExists := func(ref string) bool {
		return gitCmd(ctx, dir, env, "rev-parse", "--verify", "--quiet", ref).Run() == nil
	}
	var ref string
	for _, b := range strings.Split(configured, "|") {
		b = strings.TrimSpace(b)
		if safeBranch.MatchString(b) && refExists("refs/remotes/origin/"+b) {
			ref = "refs/remotes/origin/" + b
			break
		}
	}
	if ref == "" && refExists("refs/remotes/origin/HEAD") {
		ref = "refs/remotes/origin/HEAD"
	}
	if ref == "" {
		return nil, errors.New("no origin start branch")
	}
	cmd := gitCmd(ctx, dir, env, "show", ref+":"+file)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// shortcut: lock files are well under 16MB; raise the cap if a project ever exceeds it
	const limit = 16 << 20
	data, readErr := io.ReadAll(io.LimitReader(out, limit+1))
	if len(data) > limit {
		cmd.Process.Kill() // nobody reads the rest: do not wait for git to block on a full pipe
		cmd.Wait()
		return nil, errors.New("composer.lock is larger than 16MB")
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	return data, waitErr
}

// FetchedWithin reports whether origin was fetched (by anyone) in the last d, going by git's own
// FETCH_HEAD, so no state of ours is needed and a fetch the user ran by hand counts too.
func FetchedWithin(dir string, d time.Duration) bool {
	out, err := gitCmd(context.Background(), dir, EnrichedEnv(), "rev-parse", "--git-path", "FETCH_HEAD").Output()
	if err != nil {
		return false
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	fi, err := os.Stat(p)
	return err == nil && time.Since(fi.ModTime()) < d
}
