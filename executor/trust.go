package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var trustMu sync.Mutex

// trustFolder marks the project's git root as trusted in ~/.claude.json so Claude skips
// the "trust this folder" dialog. Trust is keyed by git root, not by subfolder.
func trustFolder(path string) error {
	root := path
	if out, err := git(path, "rev-parse", "--show-toplevel"); err == nil && strings.TrimSpace(out) != "" {
		root = strings.TrimSpace(out)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	file := filepath.Join(home, ".claude.json")

	trustMu.Lock()
	defer trustMu.Unlock()

	// Claude may save the file between our read and rename; if it changed, start over so its write isn't lost.
	for attempt := 0; attempt < 3; attempt++ {
		if changed, err := setTrusted(file, root); err != nil || !changed {
			return err
		}
	}
	return fmt.Errorf("%s kept changing while marking %s as trusted", file, root)
}

// setTrusted writes the trust flag for root; changed reports the file was modified by someone else mid-update.
func setTrusted(file, root string) (changed bool, err error) {
	doc := map[string]any{}
	mode := os.FileMode(0o600)
	orig, err := os.ReadFile(file)
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(orig))
		dec.UseNumber() // keep big numbers exact on rewrite
		if err := dec.Decode(&doc); err != nil {
			return false, fmt.Errorf("parse %s: %w", file, err) // never overwrite a file we cannot read
		}
		if st, err := os.Stat(file); err == nil {
			mode = st.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}

	projects, _ := doc["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		doc["projects"] = projects
	}
	entry, _ := projects[root].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[root] = entry
	}
	if ok, _ := entry["hasTrustDialogAccepted"].(bool); ok {
		return false, nil
	}
	entry["hasTrustDialogAccepted"] = true

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	tmp := file + ".drupal-updater.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return false, err
	}
	// shortcut: a write landing between this check and the rename is still lost (microseconds); use file locking if Claude ever adopts it.
	if now, err := os.ReadFile(file); (err == nil) != (orig != nil) || !bytes.Equal(now, orig) {
		os.Remove(tmp)
		return true, nil
	}
	return false, os.Rename(tmp, file)
}
