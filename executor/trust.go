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
// shortcut: read-modify-write races with a running claude saving the same file, tiny window; add file locking if it bites.
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

	doc := map[string]any{}
	mode := os.FileMode(0o600)
	if data, err := os.ReadFile(file); err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber() // keep big numbers exact on rewrite
		if err := dec.Decode(&doc); err != nil {
			return fmt.Errorf("parse %s: %w", file, err) // never overwrite a file we cannot read
		}
		if st, err := os.Stat(file); err == nil {
			mode = st.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
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
		return nil
	}
	entry["hasTrustDialogAccepted"] = true

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".drupal-updater.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
