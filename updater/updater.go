// Package updater finds a newer GitHub release of the desktop app and installs it in place.
// Release assets are verified with an ed25519 signature (<asset>.sig, made by scripts/sign in CI).
package updater

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// PublicKey verifies release assets (base64, from `go run ./scripts/sign -genkey`). Empty = notify only.
const PublicKey = "7Dg3qKR9JnnwobAhG4wYlOHCBeHAsOZpVlt6Qjyae00="

const latestURL = "https://api.github.com/repos/RandalVanheede/drupal-updater/releases/latest"

var client = &http.Client{Timeout: 10 * time.Minute}

type Release struct {
	Version    string `json:"version"`    // tag, e.g. v1.0.3
	URL        string `json:"url"`        // release page
	CanInstall bool   `json:"canInstall"` // signed asset for this OS/CPU and a public key to check it
	asset, sig string
	tui        bool
}

// assetSuffix is the release asset this build installs from (see .github/workflows/build.yml).
// The Linux tarball holds both apps; on macOS the TUI is a bare binary.
func assetSuffix(goos, goarch string, tui bool) string {
	switch {
	case tui && goos == "darwin":
		return "drupal-updater-darwin-" + goarch
	case goos == "darwin" && goarch == "arm64":
		return "-macos-arm64.dmg"
	case goos == "linux" && goarch == "amd64":
		return "linux-x86_64.tar.gz"
	case goos == "linux" && goarch == "arm64":
		return "linux-aarch64.tar.gz"
	}
	return ""
}

// Check returns the latest release if it is newer than current, else nil. A dev build never updates.
// tui picks the terminal app's asset instead of the desktop app's.
func Check(current string, tui bool) (*Release, error) {
	if _, ok := parse(current); !ok {
		return nil, nil
	}
	resp, err := client.Get(latestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases: %s", resp.Status)
	}
	var gh struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gh); err != nil {
		return nil, err
	}
	if !newer(gh.Tag, current) {
		return nil, nil
	}
	r := &Release{Version: gh.Tag, URL: gh.URL, tui: tui}
	if suffix := assetSuffix(runtime.GOOS, runtime.GOARCH, tui); suffix != "" {
		for _, a := range gh.Assets {
			if strings.HasSuffix(a.Name, suffix) {
				r.asset = a.URL
			}
			if strings.HasSuffix(a.Name, suffix+".sig") {
				r.sig = a.URL
			}
		}
	}
	r.CanInstall = PublicKey != "" && r.asset != "" && r.sig != ""
	return r, nil
}

func parse(v string) ([3]int, bool) {
	var n [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return n, false
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil {
			return n, false
		}
		n[i] = x
	}
	return n, true
}

func newer(latest, current string) bool {
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func download(url, dst string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Verify checks file against a base64 ed25519 signature made with the key behind pubKey.
func Verify(file, sigB64, pubKey string) error {
	pub, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid update public key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return errors.New("invalid signature file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("signature does not match: update refused")
	}
	return nil
}

// Install downloads and verifies the release, replaces the running app and returns the command that
// starts the new version; the caller runs it and quits.
func Install(r *Release) (*exec.Cmd, error) {
	if r == nil || !r.CanInstall {
		return nil, errors.New("no installable update")
	}
	dir, err := os.MkdirTemp("", "drupal-updater-update-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, filepath.Base(r.asset))
	if err := download(r.asset, file); err != nil {
		return nil, err
	}
	if err := download(r.sig, file+".sig"); err != nil {
		return nil, err
	}
	sig, _ := os.ReadFile(file + ".sig")
	if err := Verify(file, string(sig), PublicKey); err != nil {
		return nil, err
	}
	switch {
	case runtime.GOOS == "darwin" && r.tui:
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return nil, err
		}
		return replaceBinary(file, exe)
	case runtime.GOOS == "darwin":
		return installDarwin(file, dir)
	}
	return installLinux(file, dir, r.tui)
}

// replaceBinary swaps exe (the running TUI) for file, the macOS TUI release asset.
func replaceBinary(file, exe string) (*exec.Cmd, error) {
	staged := exe + ".new"
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return nil, fmt.Errorf("cannot write next to %s: %v", exe, err)
	}
	if err := os.Rename(staged, exe); err != nil { // atomic: the running process keeps its old inode
		os.Remove(staged)
		return nil, err
	}
	return exec.Command(exe), nil
}

func run(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %v\n%s", name, err, out)
	}
	return nil
}

// installDarwin swaps the running .app bundle for the one in the DMG.
func installDarwin(dmg, dir string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // X.app/Contents/MacOS/bin
	if !strings.HasSuffix(app, ".app") {
		return nil, fmt.Errorf("not running from an .app bundle (%s)", exe)
	}
	mnt := filepath.Join(dir, "mnt")
	if err := run("hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mnt, dmg); err != nil {
		return nil, err
	}
	defer run("hdiutil", "detach", "-force", mnt)
	found, _ := filepath.Glob(filepath.Join(mnt, "*.app"))
	if len(found) != 1 {
		return nil, errors.New("DMG has no single .app")
	}
	staged, old := app+".new", app+".old"
	os.RemoveAll(staged)
	os.RemoveAll(old)
	if err := run("ditto", found[0], staged); err != nil {
		return nil, err
	}
	if err := os.Rename(app, old); err != nil {
		os.RemoveAll(staged)
		return nil, err
	}
	if err := os.Rename(staged, app); err != nil {
		os.Rename(old, app) // put the running version back
		return nil, err
	}
	os.RemoveAll(old)
	return exec.Command("open", "-n", app), nil
}

// installLinux runs the tarball's install.sh (installs both apps into ~/.local/bin).
func installLinux(tarball, dir string, tui bool) (*exec.Cmd, error) {
	if err := run("tar", "-xzf", tarball, "-C", dir); err != nil {
		return nil, err
	}
	scripts, _ := filepath.Glob(filepath.Join(dir, "*", "install.sh"))
	if len(scripts) != 1 {
		return nil, errors.New("tarball has no install.sh")
	}
	if err := run("sh", scripts[0]); err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	if tui {
		return exec.Command(filepath.Join(home, ".local", "bin", "drupal-updater")), nil
	}
	return exec.Command(filepath.Join(home, ".local", "bin", "drupal-updater-desktop")), nil
}
