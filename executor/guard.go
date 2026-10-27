package executor

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"drupal-updater/logging"
)

// Claude's git and ddev calls go through read-only shims in <runDir>/bin that run the hidden
// "guard" subcommand: permission globs can't parse git's argv, and code running inside the ddev
// containers can rewrite .ddev, whose host commands and hooks ddev runs on the host.

const (
	ddevTrustedFile = "ddev-trusted.json" // host-executing .ddev states ddev may run with
	gitConfigFile   = "git-config"        // the repo's local git config as last seen by the guard
)

// Guard handles `<exe> guard git|ddev <real binary> <run dir> <args...>` and returns the exit code.
// The shims also pass DRUPAL_UPDATER_PROJECT (project root) and DRUPAL_UPDATER_GIT (real git).
func Guard(args []string) int {
	root, git := os.Getenv("DRUPAL_UPDATER_PROJECT"), os.Getenv("DRUPAL_UPDATER_GIT")
	if len(args) < 3 || !filepath.IsAbs(args[1]) || !filepath.IsAbs(root) || !filepath.IsAbs(git) {
		fmt.Fprintln(os.Stderr, "drupal-updater guard: usage: guard git|ddev <absolute binary> <run dir> [args...] (run via the shims)")
		return 2
	}
	tool, real, runDir, rest := args[0], args[1], args[2], args[3:]
	var sub string
	err := checkCwd(root)
	if err == nil {
		// hooks/filters/url rewrites in .git/config would run on the host; .git is writable from the containers
		err = checkGitConfig(git, root, runDir)
	}
	if err == nil {
		switch tool {
		case "git":
			err = checkGitArgs(rest, gitRemotes(git, root))
			sub = rest[0] // checked non-empty above
			rest = append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, rest...)
		case "ddev":
			err = checkDdev(root, runDir)
		default:
			err = fmt.Errorf("unknown tool %q", tool)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "drupal-updater guard: refused: %v\n", err)
		return 126
	}
	cmd := exec.Command(real, rest...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	runErr := cmd.Run()
	if tool == "git" {
		// git itself may legitimately change config (--set-upstream-to) and remote refs (fetch/pull).
		// shortcut: a container write landing while this git runs is accepted too; tiny window, use inotify if it matters.
		if err := saveGitConfig(git, root, runDir); err != nil {
			fmt.Fprintf(os.Stderr, "drupal-updater guard: %v\n", err)
		}
		if sub == "fetch" || sub == "pull" {
			if err := trustRemoteRefs(git, root, runDir); err != nil {
				fmt.Fprintf(os.Stderr, "drupal-updater guard: %v\n", err)
			}
		}
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "drupal-updater guard: %v\n", runErr)
		return 127
	}
	return 0
}

// checkCwd refuses commands run outside the project: ddev would pick up another .ddev.
func checkCwd(root string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cwd, _ = filepath.EvalSymlinks(cwd)
	r, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(r, cwd); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("must run inside the project %s (cwd is %s)", root, cwd)
	}
	return nil
}

func localGitConfig(git, root string) (string, error) {
	cmd := exec.Command(git, "config", "--local", "--list")
	cmd.Dir = root
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil // no local config entries
	}
	return string(out), err
}

func checkGitConfig(git, root, runDir string) error {
	current, err := localGitConfig(git, root)
	if err != nil {
		return fmt.Errorf("read git config: %w", err)
	}
	saved, err := os.ReadFile(filepath.Join(runDir, gitConfigFile))
	if err != nil {
		return fmt.Errorf("no git config snapshot: %w", err)
	}
	if current != string(saved) {
		return errors.New(".git/config was changed outside git (possibly by code in a container); a human must review it")
	}
	return nil
}

func saveGitConfig(git, root, runDir string) error {
	current, err := localGitConfig(git, root)
	if err != nil {
		return err
	}
	return writeReadOnly(filepath.Join(runDir, gitConfigFile), []byte(current))
}

func writeReadOnly(file string, data []byte) error {
	os.Remove(file) // the previous copy is read-only
	return os.WriteFile(file, data, 0o444)
}

var gitSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "rev-parse": true, "symbolic-ref": true,
	"branch": true, "checkout": true, "switch": true, "stash": true, "fetch": true, "pull": true,
	"remote": true, "merge": true, "rebase": true, "add": true, "commit": true, "restore": true,
}

// Long options that run programs, write outside the repo or reach other repositories.
// git accepts any unambiguous prefix, so every prefix of these is refused too.
var gitDangerousLong = []string{
	"upload-pack", "receive-pack", "exec", "exec-path", "output", "config-env", "ext-diff", "textconv",
	"open-files-in-pager", "git-dir", "work-tree", "namespace", "bare", "strategy", "recurse-submodules",
	"template", "separate-git-dir", "multiple", // --multiple makes fetch treat every argument as a remote
}

// Short options per subcommand that run programs (-x cmd) or pick a merge strategy binary (-s).
var gitDangerousShort = map[string]string{"rebase": "xs", "merge": "s", "pull": "s"}

// checkGitArgs validates a git argv (without "git"); remotes lists the repo's configured remotes.
func checkGitArgs(args []string, remotes []string) error {
	if len(args) == 0 || !gitSubcommands[args[0]] {
		return fmt.Errorf("git: only these subcommands are allowed, with no options before them: %s", strings.Join(slices.Sorted(maps.Keys(gitSubcommands)), " "))
	}
	sub := args[0]
	var positional []string
scan:
	for i, a := range args[1:] {
		switch {
		case a == "--":
			positional = append(positional, args[i+2:]...)
			break scan
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a[2:], "=")
			for _, d := range gitDangerousLong {
				if strings.HasPrefix(d, name) {
					return fmt.Errorf("git %s: option %s is not allowed", sub, a)
				}
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if bad := gitDangerousShort[sub]; strings.ContainsAny(a[1:], bad) {
				return fmt.Errorf("git %s: options -%s are not allowed (found in %s)", sub, bad, a)
			}
		default:
			positional = append(positional, a)
		}
	}
	switch sub {
	case "fetch", "pull":
		if len(positional) > 0 && !slices.Contains(remotes, positional[0]) {
			return fmt.Errorf("git %s: %q is not a configured remote (%s); pass option values as --opt=value", sub, positional[0], strings.Join(remotes, ", "))
		}
	case "remote":
		rest := args[1:]
		ok := len(rest) == 0 || (len(rest) == 1 && rest[0] == "-v") ||
			(len(rest) == 2 && (rest[0] == "show" || rest[0] == "get-url") && slices.Contains(remotes, rest[1]))
		if !ok {
			return errors.New("git remote: only 'git remote', 'git remote -v', 'git remote show <remote>' and 'git remote get-url <remote>' are allowed")
		}
	}
	return nil
}

func gitRemotes(git, root string) []string {
	cmd := exec.Command(git, "remote")
	cmd.Dir = root
	out, _ := cmd.Output()
	return strings.Fields(string(out))
}

// ddevSurface reports whether a project-relative path lets ddev run code on the host
// (host commands, exec-host hooks, provider scripts, compose files that can mount host paths).
func ddevSurface(rel string) bool {
	switch {
	case strings.HasPrefix(rel, ".ddev/commands/host/"), strings.HasPrefix(rel, ".ddev/providers/"):
		return true
	case rel == ".ddev/commands", rel == ".ddev/commands/host", rel == ".ddev/providers":
		return true // only reached when not a real directory, e.g. a symlink pointing elsewhere
	}
	for _, pat := range []string{".ddev/config*.yaml", ".ddev/docker-compose*.yaml"} {
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
	}
	return false
}

// workingSurface maps each host-executing .ddev file in the working tree to its git blob id.
func workingSurface(projectDir string) (map[string]string, error) {
	surface := map[string]string{}
	add := func(p string, d fs.DirEntry) error {
		rel, _ := filepath.Rel(projectDir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() || !ddevSurface(rel) {
			return nil
		}
		var data []byte
		var err error
		if d.Type()&fs.ModeSymlink != 0 {
			var target string
			target, err = os.Readlink(p) // git stores a symlink as a blob of its target
			data = []byte(target)
		} else {
			data, err = os.ReadFile(p)
		}
		if err != nil {
			return err
		}
		sum := sha1.Sum(append([]byte("blob "+strconv.Itoa(len(data))+"\x00"), data...))
		surface[rel] = hex.EncodeToString(sum[:])
		return nil
	}
	ddev := filepath.Join(projectDir, ".ddev")
	entries, err := os.ReadDir(ddev)
	if errors.Is(err, fs.ErrNotExist) {
		return surface, nil
	} else if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := add(filepath.Join(ddev, e.Name()), e); err != nil {
			return nil, err
		}
	}
	// only these two subtrees are walked: .ddev also holds large db snapshots
	for _, sub := range []string{"commands/host", "providers"} {
		err := filepath.WalkDir(filepath.Join(ddev, sub), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return add(p, d)
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return surface, nil
}

// treeSurface maps the host-executing .ddev files committed at ref to their blob ids.
func treeSurface(real, projectDir, ref string) (map[string]string, error) {
	cmd := exec.Command(real, "ls-tree", "-r", ref, "--", ".ddev")
	cmd.Dir = projectDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	surface := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		meta, rel, ok := strings.Cut(sc.Text(), "\t") // "<mode> blob <sha>\t<path>"
		if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == "blob" && ddevSurface(rel) {
			surface[rel] = f[2]
		}
	}
	return surface, nil
}

// checkDdev allows ddev only while the host-executing .ddev files match a trusted state: the one at
// run start, or a remote-tracking branch as fetched by the run start or by the guard's own fetch/pull.
// Refs are never read live: code in a container could forge them.
func checkDdev(root, runDir string) error {
	current, err := workingSurface(root)
	if err != nil {
		return err
	}
	trusted, err := readTrusted(runDir)
	if err != nil {
		return err
	}
	for _, t := range trusted {
		if maps.Equal(current, t) {
			return nil
		}
	}
	var changed []string
	for f, sum := range current {
		if trusted[0][f] != sum {
			changed = append(changed, f)
		}
	}
	for f := range trusted[0] {
		if _, ok := current[f]; !ok {
			changed = append(changed, f+" (deleted)")
		}
	}
	slices.Sort(changed)
	return fmt.Errorf("ddev: .ddev files that run on the host changed since the run started and match no fetched upstream branch: %s. A human must review them", strings.Join(changed, ", "))
}

func readTrusted(runDir string) ([]map[string]string, error) {
	var trusted []map[string]string
	data, err := os.ReadFile(filepath.Join(runDir, ddevTrustedFile))
	if err == nil {
		err = json.Unmarshal(data, &trusted)
	}
	if err == nil && len(trusted) == 0 {
		err = errors.New("empty")
	}
	if err != nil {
		return nil, fmt.Errorf("no trusted .ddev state recorded: %w", err)
	}
	return trusted, nil
}

// trustRemoteRefs adds the .ddev state of every remote-tracking branch to the trusted list.
func trustRemoteRefs(git, root, runDir string) error {
	trusted, err := readTrusted(runDir)
	if err != nil {
		return err
	}
	cmd := exec.Command(git, "for-each-ref", "--count=200", "--format=%(refname)", "refs/remotes")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	for _, ref := range strings.Fields(string(out)) {
		tree, err := treeSurface(git, root, ref)
		if err != nil || slices.ContainsFunc(trusted, func(t map[string]string) bool { return maps.Equal(t, tree) }) {
			continue
		}
		trusted = append(trusted, tree)
	}
	data, _ := json.Marshal(trusted)
	return writeReadOnly(filepath.Join(runDir, ddevTrustedFile), data)
}

// writeGuardState records the run-start state the guard checks against: the working .ddev, the
// .ddev of every remote-tracking branch, and the repo's local git config.
func writeGuardState(git, root, runDir string) error {
	start, err := workingSurface(root)
	if err != nil {
		return err
	}
	data, _ := json.Marshal([]map[string]string{start})
	if err := writeReadOnly(filepath.Join(runDir, ddevTrustedFile), data); err != nil {
		return err
	}
	if err := trustRemoteRefs(git, root, runDir); err != nil {
		logging.Error("could not record remote .ddev states: %v", err) // not a git repo yet: run-start state only
	}
	return saveGitConfig(git, root, runDir)
}
