package executor

import (
	"context"
	"errors"
	"fmt"
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

var glabBin = "glab" // tests point this at a fake

var mrURL = regexp.MustCompile(`https://\S+/merge_requests/\d+`)

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
	glab := lookEnriched(glabBin)
	if _, err := exec.LookPath(glab); err != nil {
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
	progress("glab mr create → " + target)
	cmd := exec.CommandContext(ctx, glab, "mr", "create", "--source-branch", branch, "--target-branch", target,
		"--title", msg, "--description", mrDescription(summary), "--yes")
	cmd.Dir = path
	cmd.Env = append(EnrichedEnv(), "GLAB_NO_PROMPT=1")
	out, err := cmd.CombinedOutput()
	if url := mrURL.FindString(string(out)); url != "" { // glab also prints the URL of an existing MR for this branch
		logging.Info("[%s] merge request: %s", name, url)
		return url, nil
	}
	if err != nil {
		return "", fmt.Errorf("glab mr create failed: %v\n%s", err, out)
	}
	return "", fmt.Errorf("glab gave no merge request URL:\n%s", out)
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
