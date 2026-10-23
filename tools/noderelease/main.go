package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
	"github.com/Arman2122/p-ui/internal/node/release"
)

func readKey(path string, private bool) (any, error) {
	var file *os.File
	var err error
	if private {
		file, err = privatefile.Open(path, os.O_RDONLY, 0)
	} else {
		file, err = os.Open(path)
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return nil, err
	}
	if len(data) > 8192 {
		return nil, release.ErrRelease
	}
	block, rest := pem.Decode(data)
	if block == nil || len(rest) != 0 {
		return nil, release.ErrRelease
	}
	if private {
		if block.Type != "PRIVATE KEY" {
			return nil, release.ErrRelease
		}
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if block.Type != "PUBLIC KEY" {
		return nil, release.ErrRelease
	}
	return x509.ParsePKIXPublicKey(block.Bytes)
}

func readBounded(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, release.ErrRelease
	}
	return data, nil
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: noderelease keygen|sign|verify")
	}
	flags := flag.NewFlagSet("noderelease "+args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	keyPath := flags.String("key", "", "publisher key file")
	dir := flags.String("dir", "", "directory containing the node runtime and release-verifier binaries")
	version := flags.String("release", "", "release version")
	platform := flags.String("platform", "", "target platform, such as linux-amd64")
	sequence := flags.Uint64("sequence", 0, "monotonic publisher release sequence")
	stateFormat := flags.Uint("state-format", uint(compatibility.StateFormat), "durable node state compatibility format")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return release.ErrRelease
	}
	if !filepath.IsAbs(*keyPath) {
		return errors.New("publisher key path must be absolute")
	}
	switch args[0] {
	case "keygen":
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		encoded, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return err
		}
		file, err := privatefile.Open(*keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := pem.Encode(file, &pem.Block{Type: "PRIVATE KEY", Bytes: encoded}); err != nil {
			return err
		}
		if err := file.Sync(); err != nil {
			return err
		}
		encoded, err = x509.MarshalPKIXPublicKey(public)
		if err != nil {
			return err
		}
		return pem.Encode(out, &pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
	case "sign":
		key, err := readKey(*keyPath, true)
		if err != nil {
			return err
		}
		private, ok := key.(ed25519.PrivateKey)
		if !ok {
			return release.ErrRelease
		}
		manifest, err := release.Create(*dir, *version, *platform, *sequence)
		if err != nil {
			return err
		}
		if *stateFormat == 0 || uint64(*stateFormat) > 1<<32-1 {
			return release.ErrRelease
		}
		manifest.StateFormat = uint32(*stateFormat)
		data, signature, err := release.Sign(manifest, private)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, "manifest.json"), data, 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(*dir, "manifest.sig"), signature, 0o644)
	case "verify":
		key, err := readKey(*keyPath, false)
		if err != nil {
			return err
		}
		public, ok := key.(ed25519.PublicKey)
		if !ok {
			return release.ErrRelease
		}
		data, err := readBounded(filepath.Join(*dir, "manifest.json"), 64<<10)
		if err != nil {
			return err
		}
		signature, err := readBounded(filepath.Join(*dir, "manifest.sig"), ed25519.SignatureSize)
		if err != nil {
			return err
		}
		manifest, err := release.Verify(data, signature, public, *platform)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Verified manifest for release %s, platform %s, sequence %d\n", manifest.Release, manifest.Platform, manifest.Sequence)
		return err
	default:
		return errors.New("unknown node release command")
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
