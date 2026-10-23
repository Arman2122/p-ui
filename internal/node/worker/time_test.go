package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
)

type timedEngine struct {
	applies int
	client  string
	account *limits.Account
	fail    bool
}

func (*timedEngine) Validate([]byte) error { return nil }
func (e *timedEngine) Apply(_ context.Context, _ []byte, lookup func(string) (*limits.Account, bool)) error {
	e.applies++
	if e.fail {
		return errors.New("configuration rejected")
	}
	var ok bool
	e.account, ok = lookup(e.client)
	if !ok {
		return errors.New("missing governed account")
	}
	return e.account.Check()
}
func (*timedEngine) Close() error { return nil }

func TestRuntimeWaitsForAuthenticatedTimeBeforeRestoringAuthority(t *testing.T) {
	dir := t.TempDir()
	id := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(dir, "journal.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credit, err := governor.Open(filepath.Join(dir, "credit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer credit.Close()
	at := trustedtime.Instant{Boot: uuid.NewString(), Nano: int64(time.Second)}
	clock, err := trustedtime.New(store, func() (trustedtime.Instant, error) { return at, nil })
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(time.Hour).UnixMilli()
	grant := governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, ExpiresUnixMilli: base + 60000, QuotaUnlimited: true, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
	engine := &timedEngine{client: grant.ClientID}
	process, err := New(t.Context(), "test", "test", engine, store, credit)
	if err != nil {
		t.Fatal(err)
	}
	defer process.cancel()
	if err := process.RequireClock(clock); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "test", Core: "test", Configuration: json.RawMessage(`{}`), Grants: []governor.Grant{grant}}}})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	deployment := &nodev1.Deployment{NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}
	if err := process.Apply(t.Context(), deployment); !errors.Is(err, trustedtime.ErrUntrusted) {
		t.Fatalf("untrusted boot applied authority: %v", err)
	}
	if engine.applies != 0 {
		t.Fatalf("untrusted boot reached engine %d times", engine.applies)
	}
	cursors, err := store.Cursors()
	if err != nil || cursors.Desired != 1 || cursors.Applied != 0 {
		t.Fatalf("held deployment cursors=%+v %v", cursors, err)
	}
	server := httptest.NewServer(process.Handler())
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/v1/time")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	request := new(nodev1.TimeRequest)
	if err := proto.Unmarshal(data, request); err != nil || request.NodeId != id.NodeID || request.InstallationId != id.InstallationID || request.Nonce == "" {
		t.Fatalf("runtime time challenge=%+v %v", request, err)
	}
	reply := &nodev1.TimeResponse{NodeId: uuid.NewString(), InstallationId: id.InstallationID, Nonce: request.Nonce, ServerUnixMilli: base}
	post := func(want int) {
		t.Helper()
		data, err := proto.Marshal(reply)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Post(server.URL+"/v1/time", "application/protobuf", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("time response HTTP %d, want %d", response.StatusCode, want)
		}
	}
	post(http.StatusConflict)
	if engine.applies != 0 {
		t.Fatal("foreign time response restored authority")
	}
	reply.NodeId = id.NodeID
	post(http.StatusNoContent)
	post(http.StatusConflict)
	cursors, err = store.Cursors()
	if err != nil || cursors.Applied != 1 || engine.applies != 1 {
		t.Fatalf("fresh time restore cursors=%+v applies=%d %v", cursors, engine.applies, err)
	}
	engine.fail = true
	deployment.Revision = 2
	if err := process.Apply(t.Context(), deployment); err == nil || err.Error() != "configuration rejected" {
		t.Fatalf("rejected configuration=%v", err)
	}
	response, err = server.Client().Get(server.URL + "/v1/time")
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(data, request); err != nil {
		t.Fatal(err)
	}
	reply.Nonce = request.Nonce
	post(http.StatusNoContent)
	if engine.applies != 2 {
		t.Fatal("time refresh retried a rejected configuration")
	}
	at.Nano += int64(61 * time.Second)
	if err := engine.account.Take(t.Context(), limits.Download, 1); !errors.Is(err, limits.ErrExpired) {
		t.Fatalf("runtime expiry followed wall clock: %v", err)
	}
}
