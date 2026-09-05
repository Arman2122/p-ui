package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// A client that can negotiate only one family still gets in: ed25519 for
// current clients, RSA in both its SHA-2 and legacy SHA-1 forms for the
// Android tunnel apps built on older SSH libraries.
func TestHostKeysAreOfferedInBothFamilies(t *testing.T) {
	_, inst := startCore(t, `{"transport":"tcp"}`, userN(0))
	addr := net.JoinHostPort(inst.Listen, itoa(inst.Port))
	for _, algo := range []string{gossh.KeyAlgoED25519, gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSA} {
		cfg := clientConfig("user0", "secret-0")
		cfg.HostKeyAlgorithms = []string{algo}
		client, err := gossh.Dial("tcp", addr, cfg)
		if err != nil {
			t.Fatalf("a client that only speaks %s was refused: %v", algo, err)
		}
		_ = client.Close()
	}
}

/*
An install from before RSA was offered holds one ed25519 block. It gains an
RSA key beside it, once, and keeps the ed25519 key every client has pinned;
a later load of the stored set mints and stores nothing.
*/
func TestAStoredEd25519KeyGainsAnRSAKeyOnce(t *testing.T) {
	was := rsaHostKeyBits
	rsaHostKeyBits = 2048
	t.Cleanup(func() { rsaHostKeyBits = was })

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "old")
	if err != nil {
		t.Fatal(err)
	}
	keys := &memoryHostKey{pem: string(pem.EncodeToMemory(block))}
	pinned := parseHostKeys(keys.pem).signers()[0].PublicKey()

	stores := 0
	deps := Deps{LoadHostKey: keys.load, StoreHostKey: func(p string) error { stores++; return keys.store(p) }}
	c := New(deps)
	c.mu.Lock()
	err = c.ensureHostKeyLocked()
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if stores != 1 {
		t.Fatalf("the completed set was stored %d times, want once", stores)
	}
	set := parseHostKeys(keys.pem)
	if !set.has(gossh.KeyAlgoED25519) || !set.has(gossh.KeyAlgoRSA) || len(set) != 2 {
		t.Fatalf("stored set holds %d keys, want the ed25519 key and one RSA key", len(set))
	}
	if got := c.HostKeyFingerprint(); got != gossh.FingerprintSHA256(pinned) {
		t.Fatalf("the fingerprint clients pinned changed: %s, was %s", got, gossh.FingerprintSHA256(pinned))
	}

	again := New(deps)
	again.mu.Lock()
	err = again.ensureHostKeyLocked()
	again.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if stores != 1 {
		t.Fatalf("a complete set was stored again (%d stores)", stores)
	}
}
