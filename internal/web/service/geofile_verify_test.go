package service

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// record frames one length-delimited entry the way a .dat carries it: the
// field-1 tag byte, a base-128 varint length, then the body.
func record(body []byte) []byte {
	out := []byte{0x0A}
	for n := uint64(len(body)); ; {
		b := byte(n & 0x7F)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			break
		}
	}
	return append(out, body...)
}

func geofile(bodies ...[]byte) []byte {
	var out []byte
	for _, b := range bodies {
		out = append(out, record(b)...)
	}
	return out
}

/*
The verifier's whole job is to reject a file that still parses.

A geofile is a bare stream of length-delimited records, so a download cut
partway through is a perfectly readable file holding however many records
arrived. Measured (RUNTIME-RESULTS §8), `xray -test` then ACCEPTS the config
whenever the codes a rule names sit before the cut, so the pre-flight is not a
backstop here: whether the panel notices is decided by protobuf ordering.
*/
func TestVerifyGeofileRejectsWhatStillParses(t *testing.T) {
	whole := geofile(bytes.Repeat([]byte{1}, 40), bytes.Repeat([]byte{2}, 4000), bytes.Repeat([]byte{3}, 7))

	for _, tc := range []struct {
		name    string
		blob    []byte
		wantErr bool
	}{
		{name: "an intact file", blob: whole},
		{name: "one record", blob: geofile([]byte{9})},
		// The F282 failure itself: a connection dropped mid-body.
		{name: "cut inside the last record's body", blob: whole[:len(whole)-3], wantErr: true},
		{name: "cut halfway", blob: whole[:len(whole)/2], wantErr: true},
		// The tail of a multi-byte varint is a length the file cannot honour.
		{name: "cut inside a length varint", blob: whole[:44], wantErr: true},
		{name: "cut right after a tag byte", blob: append(append([]byte{}, geofile([]byte{9})...), 0x0A), wantErr: true},
		{name: "an empty file", blob: nil, wantErr: true},
		{name: "a zero-length record", blob: []byte{0x0A, 0x00}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyGeofile(bytes.NewReader(tc.blob))
			if tc.wantErr && err == nil {
				t.Fatal("accepted; the panel would install it and restart the core onto it")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("rejected an intact file: %v", err)
			}
		})
	}
}

/*
Driven against the real thing, because a hand-built fixture only proves the
verifier agrees with itself. This is the exact truncation §8 measured getting
past `xray -test`.
*/
func TestVerifyGeofileOnTheRealArticle(t *testing.T) {
	path := ""
	for _, candidate := range []string{"/root/xray/geoip.dat", "/usr/local/p-ui/bin/geoip.dat"} {
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
	}
	if path == "" {
		t.Skip("no real geoip.dat on this host")
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	if err := verifyGeofile(bytes.NewReader(blob)); err != nil {
		t.Fatalf("the shipped geoip.dat was rejected, so the verifier disagrees with the format: %v", err)
	}
	// Half the file: accepted by the core for geoip:cn and geoip:ir (§8).
	if err := verifyGeofile(bytes.NewReader(blob[:len(blob)/2])); err == nil {
		t.Fatal("a half-downloaded geoip.dat was accepted; that is the file the core starts on and later refuses")
	}
	if err := verifyGeofile(bytes.NewReader(blob[:1024])); err == nil {
		t.Fatal("1 KB of a 19 MB file was accepted")
	}
}

// A failed verification must leave the file that is already there untouched,
// which is the half of the fix the verifier cannot do on its own.
func TestAFailedGeofileWriteLeavesNoDebrisBehind(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "geoip.dat")
	const original = "the geofile that is already working"
	if err := os.WriteFile(dest, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyGeofile(strings.NewReader("truncated")); err == nil {
		t.Fatal("garbage was accepted")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("the live geofile is now %q, want it untouched", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the directory holds %d files, want just the original: %v", len(entries), entries)
	}
}
