package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Claude's git and ddev calls go through read-only shims in <runDir>/bin that run the hidden
// "guard" subcommand. It blocks only what an update must never do: create commits or merges,
// push, and destroy local work or data. Everything else runs, so the agent can fix problems itself.
// It stops mistakes, not a determined agent: git or ddev called by full path skips the shims.

// Guard handles `<exe> guard git|ddev <real binary> <args...>` and returns the exit code.
func Guard(args []string) int {
	if len(args) < 2 || !filepath.IsAbs(args[1]) {
		fmt.Fprintln(os.Stderr, "drupal-updater guard: usage: guard git|ddev <absolute binary> [args...]")
		return 2
	}
	tool, real, rest := args[0], args[1], args[2:]
	var err error
	switch tool {
	case "git":
		err = checkGitArgs(rest, func(name string) string {
			out, _ := exec.Command(real, "config", "--get", "alias."+name).Output()
			return strings.TrimSpace(string(out))
		})
	case "ddev":
		err = checkDdevArgs(rest)
	default:
		err = fmt.Errorf("unknown tool %q", tool)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "drupal-updater guard: refused: %v\n", err)
		return 126
	}
	cmd := exec.Command(real, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "drupal-updater guard: %v\n", err)
		return 127
	}
	return 0
}

// git subcommands that create commits/merges or publish them.
var gitNoCommit = []string{"commit", "commit-tree", "merge", "cherry-pick", "revert", "am", "push"}

// git global options that take their value as the next argument.
var gitGlobalWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix"}

// checkGitArgs refuses commits, merges, pushes and commands that throw away local work.
// alias returns the expansion of a git alias, or "".
func checkGitArgs(args []string, alias func(string) string) error {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") { // skip global options to find the subcommand
		if args[i] == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "alias.") {
			return errors.New("git: defining aliases with -c is not allowed")
		}
		if slices.Contains(gitGlobalWithValue, args[i]) {
			i++
		}
		i++
	}
	if i >= len(args) {
		return nil
	}
	sub, rest := args[i], args[i+1:]
	if exp := alias(sub); exp != "" {
		if strings.HasPrefix(exp, "!") {
			return fmt.Errorf("git: shell alias %q is not allowed", sub)
		}
		f := strings.Fields(exp)
		sub, rest = f[0], append(f[1:], rest...)
	}
	has := func(flags ...string) bool {
		return slices.ContainsFunc(rest, func(a string) bool { return slices.Contains(flags, a) })
	}
	switch {
	case slices.Contains(gitNoCommit, sub):
		return fmt.Errorf("git %s is not allowed: the update must stay uncommitted on the temp branch and never be pushed", sub)
	case sub == "pull" && !has("--rebase", "-r", "--rebase=true", "--rebase=merges", "--ff-only"):
		return errors.New("git pull would create a merge commit: use git pull --rebase (or --ff-only)")
	case sub == "reset" && has("--hard", "--merge"):
		return errors.New("git reset --hard throws away local changes: stash them instead (git stash push -u)")
	case sub == "clean":
		return errors.New("git clean deletes untracked files: move or stash them instead")
	case sub == "stash" && len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear"):
		return errors.New("git stash drop/clear throws away the user's stashed work: leave stashes in place")
	}
	return nil
}

// ddev subcommands and flags that destroy the local database or snapshots.
// pflag has no abbreviations, but short flags can be grouped (-yR), so groups are checked per letter.
var ddevDestructive = map[string]struct {
	long  []string
	short string
}{
	"stop": {[]string{"remove-data"}, "R"}, "rm": {[]string{"remove-data"}, "R"}, "remove": {[]string{"remove-data"}, "R"},
	"start": {[]string{"reset-database"}, ""}, "restart": {[]string{"reset-database"}, ""},
	"snapshot": {[]string{"cleanup"}, "C"},
}

func checkDdevArgs(args []string) error {
	i := slices.IndexFunc(args, func(a string) bool { return !strings.HasPrefix(a, "-") })
	if i < 0 {
		return nil
	}
	sub := args[i]
	if sub == "delete" || sub == "clean" {
		return fmt.Errorf("ddev %s is not allowed: it removes the project's database or snapshots", sub)
	}
	d, ok := ddevDestructive[sub]
	if !ok {
		return nil // e.g. composer/drush: their flags go to the tool in the container, not to ddev
	}
	for _, a := range args[i+1:] {
		if a == "--" {
			break
		}
		if name, ok := strings.CutPrefix(a, "--"); ok {
			if name, _, _ = strings.Cut(name, "="); slices.Contains(d.long, name) {
				return fmt.Errorf("ddev %s --%s is not allowed: it destroys the local database or snapshots", sub, name)
			}
		} else if d.short != "" && strings.HasPrefix(a, "-") && strings.ContainsAny(a[1:], d.short) {
			return fmt.Errorf("ddev %s -%s is not allowed (found in %s): it destroys the local database or snapshots", sub, d.short, a)
		}
	}
	return nil
}
