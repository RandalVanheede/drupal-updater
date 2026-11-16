package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.0.3", "v1.0.2", true}, {"v1.1.0", "v1.0.9", true}, {"v1.10.0", "v1.9.0", true},
		{"v1.0.2", "v1.0.2", false}, {"v1.0.1", "v1.0.2", false}, {"v2.0.0-rc1", "v1.0.0", false}, {"v1.0.3", "dev", false},
	} {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%s, %s) = %v", c.latest, c.current, got)
		}
	}
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := filepath.Join(t.TempDir(), "a.dmg")
	os.WriteFile(f, []byte("payload"), 0o644)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("payload")))
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := Verify(f, sig+"\n", pubB64); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(f, []byte("tampered"), 0o644)
	if Verify(f, sig, pubB64) == nil {
		t.Fatal("tampered file accepted")
	}
}

func TestAssetSuffix(t *testing.T) {
	if assetSuffix("darwin", "arm64") != "-macos-arm64.dmg" || assetSuffix("linux", "amd64") != "linux-x86_64.tar.gz" || assetSuffix("darwin", "amd64") != "" {
		t.Fatal("wrong asset mapping")
	}
}
