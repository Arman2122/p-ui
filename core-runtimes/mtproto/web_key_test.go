package mtproto

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWEBSigningAuthoritySurvivesRestartAndRefusesCorruption(t *testing.T) {
	directory := t.TempDir()
	first, err := webSigner(directory, "stable-inbound")
	if err != nil {
		t.Fatal(err)
	}
	token, err := first.Mint(1)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := webSigner(directory, "stable-inbound")
	if err != nil || !restarted.Verify(token, 1) {
		t.Fatalf("restart replaced WEB signing authority: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(directory, "web-*.key"))
	if err != nil || len(files) != 1 {
		t.Fatalf("WEB key files=%v, %v", files, err)
	}
	broken := bytes.Repeat([]byte{7}, 31)
	if err := os.WriteFile(files[0], broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := webSigner(directory, "stable-inbound"); err == nil {
		t.Fatal("corrupt WEB signing key was silently replaced")
	}
	got, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(got, broken) {
		t.Fatalf("corrupt signing key was modified: %v", err)
	}
}

func TestConcurrentWEBSigningKeyCreationKeepsOneAuthority(t *testing.T) {
	directory := t.TempDir()
	tokens := make([]string, 16)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range tokens {
		group.Go(func() {
			<-start
			signer, err := webSigner(directory, "shared-inbound")
			if err != nil {
				t.Error(err)
				return
			}
			token, err := signer.Mint(1)
			if err != nil {
				t.Error(err)
				return
			}
			tokens[i] = token
		})
	}
	close(start)
	group.Wait()
	signer, err := webSigner(directory, "shared-inbound")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if !signer.Verify(token, 1) {
			t.Fatal("concurrent initialization created multiple WEB signing authorities")
		}
	}
}
