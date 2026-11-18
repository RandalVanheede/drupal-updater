package executor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"drupal-updater/config"
	"drupal-updater/logging"
)

// conflictPrompt drives the Claude session that resolves a merge of the target into the update branch.
// The app starts the merge and commits it; the guard still forbids Claude to commit, merge or push.
const conflictPrompt = `You are resolving a merge conflict in the Drupal project "{{project}}" at {{path}} (your working directory). Work fully autonomously: nobody is watching, so never ask questions.

Branch {{branch}} holds a committed dependency update (summary below). To open a merge request into {{target_branch}}, the app ran git merge --no-commit origin/{{target_branch}}, which stopped with conflicts. Resolve them so the result keeps everything from {{target_branch}} plus the update.
- composer.json: merge both sides by hand.
- composer.lock: never edit by hand. Take the target's version (git checkout --theirs composer.lock), then redo the update's package changes on top of it: ddev composer update <the updated packages from the summary> --with-all-dependencies, staying within each package's major. If only the content-hash conflicts, ddev composer update --lock.
- config/sync and other files: merge both sides by meaning; keep the target's unrelated changes.
- Then make sure ddev runs (ddev describe, ddev start), and run: ddev composer install, ddev drush deploy -y, ddev drush cex -y (ddev drush cst must then show nothing pending), ddev drush status, ddev drush cr, and the front page and /user/login must return 200 without errors.
- git add every resolved file; git diff --name-only --diff-filter=U must print nothing.

Rules (enforced): do NOT commit, merge, push or abort the merge; the app commits when you report success. Stay on branch {{branch}}. Never throw away local work or data.

Update summary:
{{summary}}

Finish by writing the file {{result_file}} containing JSON only:
{"status": "success" | "failed", "summary": "<how each conflict was resolved and what you checked; or what failed and why>"}`

var glabBin, ghBin = "glab", "gh" // tests point these at fakes

var (
	ghSlug   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	ghPRURL  = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/\d+$`)
	hostName = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

var mrURL = regexp.MustCompile(`https://\S+/(merge_requests|pull)/\d+`)

// MergeRequest commits a renamed update branch's leftover changes, pushes it and opens a GitLab merge request
// into the project's merge target (default: its start branch) with glab, which picks the GitLab host from the
// remote. A conflict with the target is merged in and resolved by a Claude session first. progress gets each
// step; screens get the live Claude terminal. Returns the merge request URL.
func MergeRequest(ctx context.Context, name, path, branch string, pc config.ProjectConfig, summary string, progress func(string), screens chan<- StatusUpdate) (string, error) {
	target := pc.GitMergeTarget
	if target == "" {
		target = ResolveStartBranch(path, pc.GitStartBranch)
	}
	if !safeBranch.MatchString(branch) || !safeBranch.MatchString(target) {
		return "", fmt.Errorf("refusing unsafe branch name %q / %q", branch, target)
	}
	// never commit to or push a shared branch, or a run's temp branch
	if branch == target || slices.Contains(strings.Split(strings.ReplaceAll(pc.GitStartBranch, " ", ""), "|"), branch) || branch == ResolveStartBranch(path, pc.GitStartBranch) || strings.HasPrefix(branch, "drupal-updater/temp-") {
		return "", fmt.Errorf("refusing to open a merge request from %q: pick the renamed update branch", branch)
	}
	if _, err := git(path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return "", fmt.Errorf("branch %q does not exist locally", branch)
	}
	// GitHub remotes get a pull request through gh, everything else a GitLab merge request through glab.
	// shortcut: only github.com counts as GitHub; GitHub Enterprise hosts would need GH_HOST.
	origin, _ := git(path, "remote", "get-url", "--push", "origin") // where the branch is pushed, so the PR matches it
	origin = strings.TrimSpace(origin)
	github := remoteHost(origin) == "github.com"
	slug := repoSlug(origin)
	if github && (!ghSlug.MatchString(slug) || strings.HasPrefix(slug, ".") || strings.Contains(slug, "..")) {
		return "", fmt.Errorf("origin %q is not a github.com owner/repo URL", origin)
	}
	tool := lookEnriched(glabBin)
	if github {
		tool = lookEnriched(ghBin)
	}
	if _, err := exec.LookPath(tool); err != nil {
		if github {
			return "", errors.New("gh not found: this project is on GitHub; install it (brew install gh) and run gh auth login")
		}
		return "", errors.New("glab not found: install it (brew install glab) and run glab auth login for each GitLab host")
	}
	run := func(args ...string) (string, error) {
		out, err := git(path, args...)
		if err != nil {
			return out, fmt.Errorf("git %s failed: %v\n%s", args[0], err, out)
		}
		return out, nil
	}
	msg := strings.ReplaceAll(pc.GitCommitMessage, "{date}", time.Now().Format("2006-01-02"))

	st, _ := git(path, "status", "--porcelain")
	if cur, _ := git(path, "branch", "--show-current"); strings.TrimSpace(cur) != branch {
		if strings.TrimSpace(st) != "" { // that work belongs to another branch: never carry it onto this one
			return "", fmt.Errorf("repo is on %q with uncommitted changes; commit or stash them first", strings.TrimSpace(cur))
		}
		progress("checkout " + branch)
		if _, err := run("checkout", branch, "--"); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(st) != "" {
		var untracked []string
		for _, l := range strings.Split(st, "\n") {
			if f, ok := strings.CutPrefix(l, "?? "); ok {
				untracked = append(untracked, f)
			}
		}
		progress("commit: " + msg)
		if len(untracked) > 0 {
			progress("new files committed: " + strings.Join(untracked, ", "))
		}
		if _, err := run("add", "-A"); err != nil {
			return "", err
		}
		if _, err := run("commit", "-m", msg); err != nil {
			return "", err
		}
	}

	progress("fetch origin/" + target)
	if _, err := run("fetch", "origin", "+refs/heads/"+target+":refs/remotes/origin/"+target); err != nil {
		return "", err
	}
	// dry run: exit 1 means conflicts, anything else non-zero is a real error (e.g. git older than 2.38)
	if out, err := git(path, "merge-tree", "--write-tree", "origin/"+target, "HEAD"); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return "", fmt.Errorf("git merge-tree failed (needs git 2.38+): %v\n%s", err, out)
		}
		if err := resolveConflicts(ctx, name, path, branch, target, summary, progress, screens); err != nil {
			return "", err
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	progress("push " + branch)
	if _, err := run("push", "-u", "origin", branch); err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	if github {
		progress("gh pr create → " + target)
		// --repo pins origin: on its own gh prefers an "upstream" remote, which would open the PR on someone else's repo
		// the host is pinned too, so gh never talks to another server; it runs outside the repo, whose git config it
		// would otherwise read through its own git calls
		cmd = exec.CommandContext(ctx, tool, "pr", "create", "--repo", "github.com/"+slug, "--head", branch, "--base", target,
			"--title", msg, "--body", mrDescription(summary))
		cmd.Env = append(EnrichedEnv(), "GH_PROMPT_DISABLED=1")
		cmd.Dir = os.TempDir()
	} else {
		progress("glab mr create → " + target)
		cmd = exec.CommandContext(ctx, tool, "mr", "create", "--source-branch", branch, "--target-branch", target,
			"--title", msg, "--description", mrDescription(summary), "--yes")
		cmd.Env = append(EnrichedEnv(), "GLAB_NO_PROMPT=1")
		cmd.Dir = path
	}
	out, err := cmd.CombinedOutput()
	if url := mrURL.FindString(string(out)); url != "" && (!github || ghPRURL.MatchString(url)) { // glab also prints the URL of an existing MR for this branch
		logging.Info("[%s] merge request: %s", name, url)
		return url, nil
	}
	if err != nil {
		if github {
			return "", fmt.Errorf("gh pr create failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "none of the git remotes") {
			host := remoteHost(origin)
			if !hostName.MatchString(host) {
				host = "<the origin's server>" // never put text from the repo's config into a command to copy
			}
			return "", fmt.Errorf("glab is not logged in to %s, the server of this project's origin. Run `glab auth login --hostname %s` in a terminal, then Retry MR (a remote that is not GitLab cannot get a merge request).\n\nglab said: %s", host, host, strings.TrimSpace(string(out)))
		}
		return "", fmt.Errorf("glab mr create failed: %v\n%s", err, out)
	}
	return "", fmt.Errorf("glab gave no merge request URL:\n%s", out)
}

// repoSlug is the owner/repo part of a remote URL.
func repoSlug(remote string) string {
	p := remote
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		p = u.Path
	} else if _, after, ok := strings.Cut(remote, ":"); ok {
		p = after
	}
	return strings.TrimSuffix(strings.Trim(p, "/"), ".git")
}

// remoteHost is the host of an https, ssh:// or scp-style (git@host:group/repo) remote URL.
func remoteHost(remote string) string {
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return u.Hostname()
	}
	if at := strings.Index(remote, "@"); at >= 0 {
		remote = remote[at+1:]
	}
	host, _, _ := strings.Cut(remote, ":")
	return host
}

// resolveConflicts merges origin/target into branch without committing, lets Claude resolve it, then commits.
// On any failure the merge is aborted, leaving the branch as it was.
func resolveConflicts(ctx context.Context, name, path, branch, target, summary string, progress func(string), screens chan<- StatusUpdate) error {
	progress("conflicts with origin/" + target + ": Claude resolves them (" + AttachCommand(name) + ")")
	git(path, "merge", "--no-ff", "--no-commit", "origin/"+target) // exits 1 on the expected conflicts
	if _, err := git(path, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err != nil {
		return errors.New("git merge did not start; resolve the conflict with origin/" + target + " by hand")
	}
	abort := func(e error) error {
		if out, err := git(path, "merge", "--abort"); err != nil {
			return fmt.Errorf("%w\ngit merge --abort also failed, check the repo by hand: %v\n%s", e, err, out)
		}
		return fmt.Errorf("%w\nmerge aborted; vendor/ and the database may still hold the merged state: run ddev composer install and check the site", e)
	}

	ch := make(chan StatusUpdate, 100)
	go func() {
		runClaude(ctx, name, path, ch, conflictPrompt, "{{branch}}", branch, "{{target_branch}}", target, "{{summary}}", summary)
		close(ch)
	}()
	var last StatusUpdate
	for u := range ch {
		if u.Screen != "" {
			select {
			case screens <- u:
			default:
			}
			continue
		}
		last = u
		if u.Status == "running" && u.Command != "" {
			progress(u.Command)
		}
	}
	if last.Status != "success" {
		return abort(fmt.Errorf("Claude could not resolve the conflicts (%s): %s", last.Status, last.Output))
	}
	if cur, _ := git(path, "branch", "--show-current"); strings.TrimSpace(cur) != branch {
		return abort(fmt.Errorf("Claude left the repo on %q, expected %q", strings.TrimSpace(cur), branch))
	}
	// check before staging anything: git add clears the unmerged state even with markers left in the file
	if u, _ := git(path, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(u) != "" {
		return abort(errors.New("unresolved files remain:\n" + u))
	}
	git(path, "add", "-A")
	if out, err := git(path, "diff", "--cached", "--check", "HEAD"); err != nil && strings.Contains(out, "conflict marker") {
		return abort(errors.New("conflict markers left:\n" + out))
	}
	if err := ctx.Err(); err != nil {
		return abort(err)
	}
	if out, err := git(path, "commit", "--no-edit"); err != nil {
		return abort(fmt.Errorf("git commit failed: %v\n%s", err, out))
	}
	progress("conflicts resolved: " + last.Output)
	return nil
}

// mrDescription keeps text that came from the update (Claude, composer, drush output) from acting on GitLab:
// a line starting with "/" would be a quick action (label, assign, retarget) and @all would notify everyone.
func mrDescription(summary string) string {
	lines := strings.Split(summary, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "/") {
			lines[i] = "\u200b" + l // zero-width space: no longer read as a command
		}
	}
	return strings.NewReplacer("@all", "@\u200ball", "@here", "@\u200bhere").Replace(strings.Join(lines, "\n"))
}
