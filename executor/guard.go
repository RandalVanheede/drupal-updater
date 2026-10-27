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
)

// Claude's git and ddev calls go through read-only shims in <runDir>/bin that run the hidden
// "guard" subcommand: permission globs can't parse git's argv, and code running inside the ddev
// containers can rewrite .ddev, whose host commands and hooks ddev runs on the host.

const ddevBaselineFile = "ddev-baseline.json"

// Guard handles `<exe> guard git|ddev <real binary> <run dir> <args...>` and returns the exit code.
func Guard(args []string) int {
	if len(args) < 3 || !filepath.IsAbs(args[1]) {
		fmt.Fprintln(os.Stderr, "drupal-updater guard: usage: guard git|ddev <absolute binary> <run dir> [args...]")
		return 2
	}
	tool, real, runDir, rest := args[0], args[1], args[2], args[3:]
	var err error
	switch tool {
	case "git":
		err = checkGitArgs(rest, gitRemotes(real))
	case "ddev":
		err = checkDdev(runDir)
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

func gitRemotes(real string) []string {
	out, _ := exec.Command(real, "remote").Output()
	return strings.Fields(string(out))
}

// ddevSurface reports whether a project-relative path lets ddev run code on the host
// (host commands, exec-host hooks, provider scripts, compose files that can mount host paths).
func ddevSurface(rel string) bool {
	if strings.HasPrefix(rel, ".ddev/commands/host/") || strings.HasPrefix(rel, ".ddev/providers/") {
		return true
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

// checkDdev allows ddev only while the host-executing .ddev files match the run's start state or
// the committed state of a remote-tracking branch (so pulling upstream changes still works).
func checkDdev(runDir string) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	current, err := workingSurface(projectDir)
	if err != nil {
		return err
	}
	var baseline map[string]string
	if data, err := os.ReadFile(filepath.Join(runDir, ddevBaselineFile)); err == nil && json.Unmarshal(data, &baseline) == nil {
		if maps.Equal(current, baseline) {
			return nil
		}
	}
	if git := os.Getenv("DRUPAL_UPDATER_GIT"); filepath.IsAbs(git) { // set by the ddev shim: PATH would resolve to the git shim
		cmd := exec.Command(git, "for-each-ref", "--count=200", "--format=%(refname)", "refs/remotes")
		cmd.Dir = projectDir
		out, _ := cmd.Output()
		for _, ref := range strings.Fields(string(out)) {
			if tree, err := treeSurface(git, projectDir, ref); err == nil && maps.Equal(current, tree) {
				return nil
			}
		}
	}
	var changed []string
	for f, sum := range current {
		if baseline[f] != sum {
			changed = append(changed, f)
		}
	}
	for f := range baseline {
		if _, ok := current[f]; !ok {
			changed = append(changed, f+" (deleted)")
		}
	}
	return fmt.Errorf("ddev: .ddev files that run on the host changed since the run started and match no upstream branch: %s. A human must review them", strings.Join(changed, ", "))
}

// writeDdevBaseline records the project's host-executing .ddev state at run start.
func writeDdevBaseline(projectDir, runDir string) error {
	surface, err := workingSurface(projectDir)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(surface)
	file := filepath.Join(runDir, ddevBaselineFile)
	os.Remove(file) // the previous run's copy is read-only
	return os.WriteFile(file, data, 0o444)
}
