package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustFolderPreservesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(home, ".claude.json")
	os.WriteFile(file, []byte(`{"userID":"x","big":12345678901234567890,"projects":{"/other":{"a":1}}}`), 0o600)

	proj := t.TempDir()
	if err := trustFolder(proj); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(proj)
	if doc["projects"].(map[string]any)[real].(map[string]any)["hasTrustDialogAccepted"] != true {
		t.Fatalf("not trusted: %s", data)
	}
	if doc["userID"] != "x" || doc["projects"].(map[string]any)["/other"] == nil {
		t.Fatal("lost existing keys")
	}
	if !json.Valid(data) || !strings.Contains(string(data), "12345678901234567890") {
		t.Fatal("big number mangled")
	}
}

func TestTrustFolderCreatesMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := trustFolder(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(home, ".claude.json")); err != nil || !strings.Contains(string(data), "hasTrustDialogAccepted") {
		t.Fatalf("not created: %v %s", err, data)
	}
}
