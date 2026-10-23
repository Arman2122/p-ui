package main

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/Arman2122/p-ui/internal/node/release"
)

func signRole[T metadata.Roles](value *metadata.Metadata[T], keys ...ed25519.PrivateKey) ([]byte, error) {
	for _, key := range keys {
		signer, err := signature.LoadSigner(key, crypto.Hash(0))
		if err != nil {
			return nil, err
		}
		if _, err := value.Sign(signer); err != nil {
			return nil, err
		}
	}
	return value.ToBytes(false)
}

func metaDigest(data []byte) *metadata.MetaFiles {
	hash := sha256.Sum256(data)
	return &metadata.MetaFiles{Version: 1, Length: int64(len(data)), Hashes: metadata.Hashes{"sha256": hash[:]}}
}

func prepareRepository(dir, bundle string) error {
	_, publisher, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publisher.Public())
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "publisher.pem"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0o644); err != nil {
		return err
	}
	metadataDir, targetsDir := filepath.Join(dir, "repository", "metadata"), filepath.Join(dir, "repository", "targets")
	for _, directory := range []string{metadataDir, targetsDir, filepath.Join(dir, "bundles")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	root := metadata.Root(time.Now().Add(24 * time.Hour))
	keys := make(map[string]ed25519.PrivateKey)
	for _, role := range []string{"root", "root2", "root3", "targets", "timestamp", "snapshot", "platform"} {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		keys[role] = key
		if role == "platform" {
			continue
		}
		public, err := metadata.KeyFromPublicKey(key.Public())
		if err != nil {
			return err
		}
		name := role
		if role == "root2" || role == "root3" {
			name = "root"
		}
		if err := root.Signed.AddKey(public, name); err != nil {
			return err
		}
	}
	root.Signed.Roles["root"].Threshold = 2
	rootData, err := signRole(root, keys["root"], keys["root2"])
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "root.json"), rootData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "1.root.json"), rootData, 0o644); err != nil {
		return err
	}
	top := metadata.Targets(time.Now().Add(3 * time.Hour))
	top.Signed.Delegations = &metadata.Delegations{Keys: make(map[string]*metadata.Key), Roles: []metadata.DelegatedRole{{Name: "platform", Threshold: 1, Terminating: true, Paths: []string{"nodes/linux-amd64/*/*"}}}}
	platformKey, err := metadata.KeyFromPublicKey(keys["platform"].Public())
	if err != nil {
		return err
	}
	if err := top.Signed.AddKey(platformKey, "platform"); err != nil {
		return err
	}
	delegated := metadata.Targets(time.Now().Add(3 * time.Hour))
	for sequence := uint64(1); sequence <= 4; sequence++ {
		name := strconv.FormatUint(sequence, 10)
		destination := filepath.Join(dir, "bundles", name)
		if err := os.Mkdir(destination, 0o755); err != nil {
			return err
		}
		for _, binary := range []string{"penhoon-agent", "penhoon-xray", "penhoon-ssh", "penhoon-singbox", "penhoon-mtproto", "penhoon-native", "penhoon-release"} {
			if sequence == 4 && binary == "penhoon-xray" {
				if err := os.WriteFile(filepath.Join(destination, binary), []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
					return err
				}
				continue
			}
			if err := os.Link(filepath.Join(bundle, binary), filepath.Join(destination, binary)); err != nil {
				return err
			}
		}
		manifest, err := release.Create(destination, "guest-qualification-"+name, "linux-amd64", sequence)
		if err != nil {
			return err
		}
		encoded, signed, err := release.Sign(manifest, publisher)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "manifest.json"), encoded, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, "manifest.sig"), signed, 0o644); err != nil {
			return err
		}
		manifestHash := sha256.Sum256(encoded)
		assets := append(manifest.Assets, release.Asset{Name: "manifest.json", Size: int64(len(encoded)), SHA256: hex.EncodeToString(manifestHash[:])})
		for _, asset := range assets {
			target := "nodes/linux-amd64/" + name + "/" + asset.Name
			digest, err := hex.DecodeString(asset.SHA256)
			if err != nil {
				return err
			}
			delegated.Signed.Targets[target] = &metadata.TargetFiles{Length: asset.Size, Hashes: metadata.Hashes{"sha256": digest}}
			artifact := filepath.Join(targetsDir, filepath.FromSlash(path.Dir(target)), asset.SHA256+"."+asset.Name)
			if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(destination, asset.Name), artifact); err != nil {
				return err
			}
		}
	}
	topData, err := signRole(top, keys["targets"])
	if err != nil {
		return err
	}
	delegatedData, err := signRole(delegated, keys["platform"])
	if err != nil {
		return err
	}
	snapshot := metadata.Snapshot(time.Now().Add(3 * time.Hour))
	snapshot.Signed.Meta["targets.json"], snapshot.Signed.Meta["platform.json"] = metaDigest(topData), metaDigest(delegatedData)
	snapshotData, err := signRole(snapshot, keys["snapshot"])
	if err != nil {
		return err
	}
	timestamp := metadata.Timestamp(time.Now().Add(2 * time.Hour))
	timestamp.Signed.Meta["snapshot.json"] = metaDigest(snapshotData)
	timestampData, err := signRole(timestamp, keys["timestamp"])
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{"1.targets.json": topData, "1.platform.json": delegatedData, "1.snapshot.json": snapshotData, "timestamp.json": timestampData} {
		if err := os.WriteFile(filepath.Join(metadataDir, name), data, 0o644); err != nil {
			return err
		}
	}
	fmt.Println("Prepared disposable qualification signatures and enrollment material.")
	return nil
}
