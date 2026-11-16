// Signs release assets for the desktop auto-updater (updater.Verify).
//
//	go run ./scripts/sign -genkey        prints a new key pair (once; private key → GitHub secret UPDATE_SIGNING_KEY)
//	UPDATE_SIGNING_KEY=... go run ./scripts/sign FILE...   writes FILE.sig for each FILE
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-genkey" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail(err)
		}
		fmt.Println("private (GitHub secret UPDATE_SIGNING_KEY):", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Println("public  (updater.PublicKey):              ", base64.StdEncoding.EncodeToString(pub))
		return
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("UPDATE_SIGNING_KEY"))
	if err != nil || len(seed) != ed25519.SeedSize {
		fail(fmt.Errorf("UPDATE_SIGNING_KEY must be the base64 private key from -genkey"))
	}
	key := ed25519.NewKeyFromSeed(seed)
	for _, f := range os.Args[1:] {
		data, err := os.ReadFile(f)
		if err != nil {
			fail(err)
		}
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))
		if err := os.WriteFile(f+".sig", []byte(sig+"\n"), 0o644); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}
