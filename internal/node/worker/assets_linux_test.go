//go:build linux

package worker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/core"
	sshruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/ssh"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func testAsset(id string, data []byte) configuration.FileAsset {
	digest := sha256.Sum256(data)
	return configuration.FileAsset{ID: id, SHA256: hex.EncodeToString(digest[:]), Data: data}
}

func assetCertificate(t *testing.T, serial int64) (configuration.FileAsset, configuration.FileAsset) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "node fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return testAsset("cert", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), testAsset("key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func assetDeployment(t *testing.T, store *state.Store, revision uint64, runtime *configuration.Runtime) *nodev1.Deployment {
	t.Helper()
	bundle := configuration.Bundle{Version: 1}
	if runtime != nil {
		bundle.Runtimes = []configuration.Runtime{*runtime}
	}
	payload, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	id := store.Identity()
	return &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: revision, Payload: payload, PayloadSha256: digest[:]}
}

func TestSSHFileAssetsValidateBeforeActivationAndSurviveRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "custom-runtime-state")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(directory, "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	credit, err := governor.Open(filepath.Join(directory, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = credit.Close() }()
	engine := new(sshruntime.Runtime)
	engine.SetStateStore(directory, store)
	process, err := New(t.Context(), "ssh", "ssh", engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	process.SetAssetDirectory(directory)
	defer func() {
		process.cancel()
		_ = engine.Close()
		if process.assets != nil {
			_ = process.assets.Close()
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address, port := listener.Addr().String(), listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	firstCert, firstKey := assetCertificate(t, 1)
	secondCert, secondKey := assetCertificate(t, 2)
	runtime := func(cert, key configuration.FileAsset) configuration.Runtime {
		settings, _ := json.Marshal(map[string]any{"transport": "wss", "path": "/tunnel", "certFile": configuration.AssetReference(cert.ID, cert.SHA256), "keyFile": configuration.AssetReference(key.ID, key.SHA256)})
		encoded, _ := json.Marshal(map[string]any{"inbounds": []core.Instance{{ID: 1, Kind: "ssh", Tag: "asset-test", Listen: "127.0.0.1", Port: port, Enable: true, Settings: string(settings)}}})
		return configuration.Runtime{ID: "ssh", Core: "ssh", Configuration: encoded, FileAssets: []configuration.FileAsset{cert, key}}
	}
	assertServing := func(cert configuration.FileAsset) {
		t.Helper()
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cert.Data) {
			t.Fatal("fixture certificate")
		}
		conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}).DialContext(t.Context(), "tcp", address)
		if err != nil {
			t.Fatalf("active WSS certificate unavailable: %v", err)
		}
		_ = conn.Close()
	}
	assertFile := func(asset configuration.FileAsset, present bool) {
		t.Helper()
		path := filepath.Join(directory, "assets", asset.SHA256)
		data, err := os.ReadFile(path)
		if !present {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unretained asset remains: %s %v", asset.ID, err)
			}
			return
		}
		info, statErr := os.Stat(path)
		if err != nil || statErr != nil || !bytes.Equal(data, asset.Data) || info.Mode().Perm() != 0o600 {
			t.Fatalf("immutable material differs: %s %v %v", asset.ID, err, statErr)
		}
	}
	first := runtime(firstCert, firstKey)
	deployment := assetDeployment(t, store, 1, &first)
	if err := process.Apply(t.Context(), deployment); err != nil {
		t.Fatal(err)
	}
	assertServing(firstCert)
	assertFile(firstCert, true)
	assertFile(firstKey, true)
	for _, mode := range []string{"digest", "mismatched pair", "absolute path", "unused"} {
		t.Run(mode, func(t *testing.T) {
			candidate := runtime(secondCert, secondKey)
			switch mode {
			case "digest":
				candidate.FileAssets[0].SHA256 = firstCert.SHA256
			case "mismatched pair":
				candidate = runtime(secondCert, firstKey)
			case "absolute path":
				candidate.Configuration = bytes.ReplaceAll(candidate.Configuration, []byte(configuration.AssetReference(secondCert.ID, secondCert.SHA256)), []byte("/master/server.crt"))
			case "unused":
				candidate.FileAssets = append(candidate.FileAssets, testAsset("unused", []byte("unused material")))
			}
			if err := process.Apply(t.Context(), assetDeployment(t, store, 2, &candidate)); err == nil {
				t.Fatal("invalid candidate activated")
			}
			cursors, err := store.Cursors()
			if err != nil || cursors.Desired != 1 || cursors.Applied != 1 {
				t.Fatalf("invalid candidate changed durable state: %+v %v", cursors, err)
			}
			assertServing(firstCert)
			assertFile(firstCert, true)
			assertFile(secondCert, false)
		})
	}
	second := runtime(secondCert, secondKey)
	pending := assetDeployment(t, store, 2, &second)
	if _, err := process.desired(pending.Payload); err != nil {
		t.Fatal(err)
	}
	if err := store.Receive(pending); err != nil {
		t.Fatal(err)
	}
	if err := process.pruneAssets(); err != nil {
		t.Fatal(err)
	}
	assertFile(firstCert, true)
	assertFile(secondCert, true)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.assets.Close(); err != nil {
		t.Fatal(err)
	}
	process.cancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := credit.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(filepath.Join(directory, "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	credit, err = governor.Open(filepath.Join(directory, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine = new(sshruntime.Runtime)
	engine.SetStateStore(directory, store)
	process, err = New(t.Context(), "ssh", "ssh", engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	process.SetAssetDirectory(directory)
	desired, err := store.Desired()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := proto.Marshal(desired)
	request := httptest.NewRequest(http.MethodPost, "/v1/apply", bytes.NewReader(encoded))
	request.Header.Set("X-Pui-State-Journal", store.JournalID())
	request.Header.Set("X-Pui-Credit-Journal", credit.JournalID())
	request.Header.Set("X-Pui-Agent-Journal", uuid.NewString())
	response := httptest.NewRecorder()
	process.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get(configuration.AssetManifestHeader) != hex.EncodeToString(configuration.RuntimeAssetManifest("ssh", second.FileAssets)) {
		t.Fatalf("missing exact manifest receipt: %d %s", response.Code, response.Body.String())
	}
	assertServing(secondCert)
	assertFile(firstCert, false)
	assertFile(secondCert, true)
	if err := process.Apply(t.Context(), desired); err != nil {
		t.Fatalf("lost response replay: %v", err)
	}
	retained, err := store.Desired()
	if err != nil || !bytes.Equal(retained.Payload, pending.Payload) || bytes.Contains(retained.Payload, []byte(directory)) {
		t.Fatal("local material paths replaced signed desired bytes")
	}
	if err := process.Apply(t.Context(), assetDeployment(t, store, 3, nil)); err != nil {
		t.Fatal(err)
	}
	assertFile(secondCert, false)
	assertFile(secondKey, false)
}

type partialAssetBackend struct {
	failure error
	loaded  string
}

func (*partialAssetBackend) Validate(*configuration.Runtime) error { return nil }
func (*partialAssetBackend) Collect(context.Context) error         { return nil }
func (*partialAssetBackend) Shutdown(context.Context) error        { return nil }
func (b *partialAssetBackend) ResolveFileAssets(runtime *configuration.Runtime, resolve FileAssetResolver) (*configuration.Runtime, error) {
	var ref string
	if err := json.Unmarshal(runtime.Configuration, &ref); err != nil {
		return nil, err
	}
	path, err := resolve(ref)
	if err != nil {
		return nil, err
	}
	copy := *runtime
	copy.Configuration, _ = json.Marshal(path)
	return &copy, nil
}

func (b *partialAssetBackend) Apply(_ context.Context, runtime *configuration.Runtime) error {
	if errors.Is(b.failure, io.ErrClosedPipe) {
		return b.failure
	}
	if runtime == nil {
		b.loaded = ""
	} else if err := json.Unmarshal(runtime.Configuration, &b.loaded); err != nil {
		return err
	}
	return b.failure
}

func TestAssetCleanupRetainsPartiallyActiveConfigurationAcrossFailures(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(directory, "state.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := new(partialAssetBackend)
	process, err := NewWithBackend(t.Context(), "test", "test", store, backend, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	process.SetAssetDirectory(directory)
	defer func() {
		process.cancel()
		if process.assets != nil {
			_ = process.assets.Close()
		}
	}()
	var latest *nodev1.Deployment
	for i, name := range []string{"applied", "partial", "failed"} {
		asset := testAsset(name, []byte(name))
		ref, _ := json.Marshal(configuration.AssetReference(asset.ID, asset.SHA256))
		runtime := configuration.Runtime{ID: "test", Core: "test", Configuration: ref, FileAssets: []configuration.FileAsset{asset}}
		latest = assetDeployment(t, store, uint64(i+1), &runtime)
		backend.failure = []error{nil, io.ErrUnexpectedEOF, io.ErrClosedPipe}[i]
		if err := process.Apply(t.Context(), latest); !errors.Is(err, backend.failure) {
			t.Fatalf("apply %s: %v", name, err)
		}
		data, err := os.ReadFile(backend.loaded)
		if err != nil || i > 0 && string(data) != "partial" {
			t.Fatalf("partially active material deleted: %q %v", data, err)
		}
	}
	partialPath := backend.loaded
	backend.failure = nil
	if err := process.Apply(t.Context(), latest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("converged material retained: %v", err)
	}
}

type manyAssetBackend struct{ partialAssetBackend }

func (b *manyAssetBackend) ResolveFileAssets(runtime *configuration.Runtime, resolve FileAssetResolver) (*configuration.Runtime, error) {
	var last string
	for _, asset := range runtime.FileAssets {
		path, err := resolve(configuration.AssetReference(asset.ID, asset.SHA256))
		if err != nil {
			return nil, err
		}
		last = path
	}
	copy := *runtime
	copy.Configuration, _ = json.Marshal(last)
	return &copy, nil
}

func TestAssetUncertaintyBoundRefusesBeforeDesiredChange(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(directory, "state.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backend := new(manyAssetBackend)
	process, err := NewWithBackend(t.Context(), "test", "test", store, backend, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	process.SetAssetDirectory(directory)
	defer func() {
		process.cancel()
		if process.assets != nil {
			_ = process.assets.Close()
		}
	}()
	for attempt := 0; attempt < 3; attempt++ {
		runtime := configuration.Runtime{ID: "test", Core: "test", Configuration: json.RawMessage(`{}`)}
		count := configuration.MaxRuntimeFileAssets
		if attempt == 2 {
			count = 1
		}
		for i := 0; i < count; i++ {
			data := []byte{byte(attempt), byte(i)}
			id := hex.EncodeToString(data)
			runtime.FileAssets = append(runtime.FileAssets, testAsset(id, data))
		}
		if attempt != 0 {
			backend.failure = io.ErrUnexpectedEOF
		}
		err := process.Apply(t.Context(), assetDeployment(t, store, uint64(attempt+1), &runtime))
		if attempt < 2 {
			if !errors.Is(err, backend.failure) {
				t.Fatalf("bounded candidate: %v", err)
			}
			continue
		}
		if !errors.Is(err, configuration.ErrConfiguration) {
			t.Fatalf("uncertain material bound: %v", err)
		}
		cursors, err := store.Cursors()
		if err != nil || cursors.Desired != 2 || cursors.Applied != 1 {
			t.Fatalf("uncertainty changed desired state: %+v %v", cursors, err)
		}
		if _, err := os.ReadFile(backend.loaded); err != nil {
			t.Fatalf("uncertain active file removed: %v", err)
		}
	}
}
