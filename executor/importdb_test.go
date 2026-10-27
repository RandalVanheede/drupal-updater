package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestImportDBScript(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bin := t.TempDir()
	// stub ddev: records args instead of importing
	os.WriteFile(filepath.Join(bin, "ddev"), []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(root, "called")+"\n"), 0o755)
	script := filepath.Join(t.TempDir(), "import-db.sh")
	os.WriteFile(script, []byte(fmt.Sprintf(importDBScript, sq(root), 30)), 0o755)

	mk := func(rel string, age time.Duration) string {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		old := time.Now().Add(-age)
		os.Chtimes(p, old, old)
		return p
	}
	outside := filepath.Join(t.TempDir(), "o.sql")
	os.WriteFile(outside, []byte("x"), 0o644)
	link := filepath.Join(root, "link.sql")
	os.Symlink(outside, link)

	cases := []struct {
		name string
		file string
		ok   bool
	}{
		{"root recent", mk("a.sql.gz", time.Hour), true},
		{"db recent", mk("db/b.sql", time.Hour), true},
		{"old", mk("old.sql", 40*24*time.Hour), false},
		{"subdir", mk("sub/c.sql", time.Hour), false},
		{"outside", outside, false},
		{"symlink", link, false},
		{"traversal", root + "/db/../sub/c.sql", false},
		{"bad ext", mk("d.txt", time.Hour), false},
	}
	for _, c := range cases {
		os.Remove(filepath.Join(root, "called"))
		cmd := exec.Command(script, c.file)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		err := cmd.Run()
		_, called := os.Stat(filepath.Join(root, "called"))
		if (err == nil) != c.ok || (called == nil) != c.ok {
			t.Errorf("%s: ok=%v err=%v called=%v", c.name, c.ok, err, called == nil)
		}
	}
}
