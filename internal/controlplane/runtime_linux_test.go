package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	xrayruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/xray"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/agent"
	"github.com/Arman2122/p-ui/internal/node/command"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

func TestHeadlessRuntimeSurvivesAgentProcessDeathPostgres(t *testing.T) {
	store, db, _, clientID, termID, scopes := setupLedger(t)
	enrollment, err := store.CreateEnrollment(t.Context(), "process-test")
	if err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ControlPlaneID: uuid.NewString(), NodeID: enrollment.NodeID, InstallationID: enrollment.InstallationID}
	serverTLS, _, issuer, _ := tlsPair(t, p)
	handler, err := NewEnrollmentHandler(store, p.ControlPlaneID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := httptest.NewUnstartedServer(handler)
	bootstrap.TLS = serverTLS.Clone()
	bootstrap.StartTLS()
	defer bootstrap.Close()
	service, err := NewService(store, p.ControlPlaneID)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewGRPCServer(service, serverTLS, issuer)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-serverDone })
	dir, err := os.MkdirTemp("", "node-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	agentUID, agentGID := uint32(os.Geteuid()), uint32(os.Getegid())
	agentExecutable := os.Args[0]
	if agentUID == 0 {
		agentUID, agentGID = 65534, 65534
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		agentExecutable = filepath.Join(dir, "agent.test")
		input, err := os.Open(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(agentExecutable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		_ = input.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			t.Fatal(err)
		}
	}
	agentDir := filepath.Join(dir, "agent")
	config := credentials.Config{
		Identity: p, ControlAddress: listener.Addr().String(), ServerName: "control.test",
		EnrollmentURL: bootstrap.URL + "/enroll", TrustBundlePEM: issuer.TrustBundle,
	}
	vault, err := credentials.Open(filepath.Join(agentDir, "credentials.db"), &config)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Enroll(t.Context(), enrollment.Ticket); err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	if err := vault.Close(); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		for _, path := range []string{agentDir, filepath.Join(agentDir, "credentials.db")} {
			if err := os.Chown(path, int(agentUID), int(agentGID)); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtimeStore, err := state.Open(filepath.Join(dir, "runtime.db"), state.Identity{NodeID: p.NodeID, InstallationID: p.InstallationID, ControlPlaneID: p.ControlPlaneID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	governorStore, err := governor.Open(filepath.Join(dir, "governor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = governorStore.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	process, err := worker.New(ctx, "xray", "xray", &xrayruntime.Runtime{}, runtimeStore, governorStore)
	if err != nil {
		t.Fatal(err)
	}
	source, err := trustedtime.SystemSource()
	if err != nil {
		t.Fatal(err)
	}
	clock, err := trustedtime.New(runtimeStore, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.RequireClock(clock); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "xray.sock")
	runtimeListener, err := localrpc.Listen(ctx, socket, agentUID)
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(socket, 0, int(agentGID)); err != nil {
			t.Fatal(err)
		}
	}
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- process.Run(ctx, runtimeListener) }()
	t.Cleanup(func() {
		cancel()
		if err := <-runtimeDone; err != nil {
			t.Error(err)
		}
	})
	endpoints, err := json.Marshal([]agent.RuntimeEndpoint{{ID: "xray", Core: "xray", Socket: socket, UID: uint32(os.Getuid())}})
	if err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(dir, "runtimes.json")
	if err := os.WriteFile(registry, endpoints, 0o644); err != nil {
		t.Fatal(err)
	}
	startAgent := func(name string) func() {
		t.Helper()
		log, err := os.Create(filepath.Join(dir, name+".log"))
		if err != nil {
			t.Fatal(err)
		}
		process := exec.CommandContext(t.Context(), agentExecutable, "-test.run=^TestHeadlessAgentProcessHelper$")
		process.Env = []string{"PATH=" + os.Getenv("PATH"), "PENHOON_TEST_AGENT_DIR=" + agentDir, "PENHOON_TEST_AGENT_RUNTIMES=" + registry}
		if os.Geteuid() == 0 {
			process.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: agentUID, Gid: agentGID}}
		}
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		var once sync.Once
		stop := func() { once.Do(func() { _ = process.Process.Kill(); _ = process.Wait(); _ = log.Close() }) }
		t.Cleanup(stop)
		return stop
	}
	stopFirst := startAgent("first-agent")
	await(t, func() (bool, error) {
		var count int64
		err := db.Model(&model.FleetRuntimeJournal{}).Where("node_id = ? AND runtime_id = ?", p.NodeID, "xray").Count(&count).Error
		return count == 1, err
	})
	grant := reserve(t, store, p, clientID, termID, scopes, 1000000)
	rateBudget := uuid.NewString()
	rates := limitedRates(100_000_000, 65536)
	if err := store.SetRateBudget(t.Context(), rateBudget, clientID, termID, 1, rates); err != nil {
		t.Fatal(err)
	}
	share, err := store.RequestRateShare(t.Context(), RateRequest{
		OperationID: uuid.NewString(), GrantID: grant.GrantID, BudgetID: rateBudget,
		NodeID: p.NodeID, InstallationID: p.InstallationID, RuntimeID: "xray", Desired: rates,
	})
	if err != nil {
		t.Fatal(err)
	}

	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	xrayConfig, err := json.Marshal(map[string]any{
		"log": map[string]string{"loglevel": "none"},
		"inbounds": []map[string]any{{
			"tag": "client-in", "listen": "127.0.0.1", "port": port, "protocol": "socks",
			"settings": map[string]any{"auth": "password", "accounts": []map[string]string{{"user": clientID, "pass": "local-test"}}},
		}},
		"outbounds": []map[string]any{{"protocol": "freedom", "tag": "direct", "settings": map[string]any{
			"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{
		ID: "xray", Core: "xray", Configuration: xrayConfig,
		Grants: []governor.Grant{{ID: grant.GrantID, ClientID: clientID, RateRevision: uint64(share.Revision), Policy: limits.Policy{
			Term: termID, Revision: 1, Enabled: true,
			CreditCeiling: 1000000, Upload: rates.Upload, Download: rates.Download,
		}}},
	}}}
	if _, err := service.PublishConfiguration(t.Context(), p, bundle); err != nil {
		t.Fatal(err)
	}
	if revision, err := service.PublishConfiguration(t.Context(), p, bundle); err != nil || revision != 1 {
		t.Fatalf("identical publication=%d,%v", revision, err)
	}
	await(t, func() (bool, error) {
		var node model.FleetNode
		err := db.First(&node, "id = ?", p.NodeID).Error
		return node.AppliedRevision == 1, err
	})
	if err := db.First(&share, "id = ?", grant.GrantID).Error; err != nil {
		t.Fatal(err)
	}
	if share.AppliedRevision != share.Revision {
		t.Fatal("runtime rate application was not acknowledged")
	}
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		connection, err := echo.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), &proxy.Auth{User: clientID, Password: "local-test"}, &net.Dialer{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := dialer.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close(); _ = echo.Close(); <-echoDone }()
	transfer := func(n int) {
		t.Helper()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		data := bytes.Repeat([]byte("x"), n)
		if _, err := connection.Write(data); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, n)
		if _, err := io.ReadFull(connection, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, got) {
			t.Fatal("forwarded payload changed")
		}
	}
	transfer(16)
	stopFirst()
	transfer(32)
	stopSecond := startAgent("second-agent")
	defer stopSecond()
	transfer(48)
	await(t, func() (bool, error) {
		var saved model.FleetGrant
		err := db.First(&saved, "id = ?", grant.GrantID).Error
		if saved.ConsumedBytes > 192 {
			return false, errors.New("usage was billed more than once")
		}
		return saved.ConsumedBytes == 192, err
	})
}

func TestHeadlessAgentProcessHelper(t *testing.T) {
	dir := os.Getenv("PENHOON_TEST_AGENT_DIR")
	if dir == "" {
		return
	}
	err := command.Agent(context.Background(), []string{"run", "--state-dir", dir, "--runtimes", os.Getenv("PENHOON_TEST_AGENT_RUNTIMES")}, bytes.NewReader(nil), os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
}
