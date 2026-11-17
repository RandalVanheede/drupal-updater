package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitCmd is git with hooks and fsmonitor off, like git(), but cancellable and with separate stdout.
func gitCmd(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// FetchOrigin refreshes origin's remote-tracking branches (no tags, no submodules). It never waits on a
// prompt (no terminal, ssh BatchMode unless GIT_SSH_COMMAND is set) and stops when ctx ends.
func FetchOrigin(ctx context.Context, dir string) error {
	env := EnrichedEnv()
	// the env var would override a core.sshCommand the user set (a specific key, say), so only fill the gap
	if os.Getenv("GIT_SSH_COMMAND") == "" && gitCmd(ctx, dir, env, "config", "--get", "core.sshCommand").Run() != nil {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10")
	}
	return gitCmd(ctx, dir, env, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "origin").Run()
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
	data, readErr := io.ReadAll(io.LimitReader(out, 16<<20))
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
