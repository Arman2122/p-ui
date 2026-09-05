package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"sort"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/logger"
)

/*
The node's host keys, as the setting stores them: PEM blocks back to back.

Two are offered. ed25519 is what every current client prefers; RSA is there
because the Android tunnel apps built on older JSch cannot negotiate ed25519
at all and fail with "algorithm negotiation fail" against a server offering
nothing else, which is a server they cannot use. OpenSSH offers both too.
*/

// rsaHostKeyBits is the RSA host key's size; a var so a test can shrink it.
var rsaHostKeyBits = 3072

// hostKeyAlgos is the set a node offers, in the order clients are shown them.
var hostKeyAlgos = []string{gossh.KeyAlgoED25519, gossh.KeyAlgoRSA}

/*
PreferredHostKeyAlgorithms is the order this panel negotiates a REMOTE server's
host key in when it is the client: the exits, and the fingerprint scan behind
the "Fetch from server" button. ed25519 first, so the fingerprint an operator
is shown, fetches and pins is the key the exit then connects with. x/crypto's
own default puts ECDSA and RSA ahead of it, and against a server offering both
families that pinned an ed25519 key and connected with RSA.
*/
var PreferredHostKeyAlgorithms = []string{
	gossh.KeyAlgoED25519,
	gossh.KeyAlgoECDSA256, gossh.KeyAlgoECDSA384, gossh.KeyAlgoECDSA521,
	gossh.KeyAlgoRSASHA256, gossh.KeyAlgoRSASHA512,
}

type hostKey struct {
	signer gossh.Signer
	pem    []byte
}

type hostKeySet []hostKey

// parseHostKeys keeps every block that parses; a block that does not is
// dropped with a warning rather than taking the whole set down with it.
func parseHostKeys(stored string) hostKeySet {
	var keys hostKeySet
	rest := []byte(stored)
	for {
		block, more := pem.Decode(rest)
		if block == nil {
			return keys
		}
		rest = more
		encoded := pem.EncodeToMemory(block)
		signer, err := gossh.ParsePrivateKey(encoded)
		if err != nil {
			logger.Warning("ssh: a stored host key does not parse and is dropped:", err)
			continue
		}
		keys = append(keys, hostKey{signer: signer, pem: encoded})
	}
}

func (s hostKeySet) has(algo string) bool {
	for _, k := range s {
		if k.signer.PublicKey().Type() == algo {
			return true
		}
	}
	return false
}

func (s *hostKeySet) mint(algo string) error {
	var priv any
	switch algo {
	case gossh.KeyAlgoED25519:
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		priv = key
	case gossh.KeyAlgoRSA:
		key, err := rsa.GenerateKey(rand.Reader, rsaHostKeyBits)
		if err != nil {
			return err
		}
		priv = key
	default:
		return fmt.Errorf("ssh: no host key of type %s can be minted", algo)
	}
	block, err := gossh.MarshalPrivateKey(priv, "p-ui")
	if err != nil {
		return err
	}
	encoded := pem.EncodeToMemory(block)
	signer, err := gossh.ParsePrivateKey(encoded)
	if err != nil {
		return err
	}
	*s = append(*s, hostKey{signer: signer, pem: encoded})
	return nil
}

func (s hostKeySet) encode() string {
	var out []byte
	for _, k := range s {
		out = append(out, k.pem...)
	}
	return string(out)
}

// signers returns the keys in hostKeyAlgos order, so the first is the ed25519
// key whose fingerprint the panel shows and the exits pin.
func (s hostKeySet) signers() []gossh.Signer {
	rank := func(k hostKey) int {
		for i, algo := range hostKeyAlgos {
			if k.signer.PublicKey().Type() == algo {
				return i
			}
		}
		return len(hostKeyAlgos)
	}
	ordered := append(hostKeySet(nil), s...)
	sort.SliceStable(ordered, func(i, j int) bool { return rank(ordered[i]) < rank(ordered[j]) })
	out := make([]gossh.Signer, 0, len(ordered))
	for _, k := range ordered {
		out = append(out, k.signer)
	}
	return out
}
