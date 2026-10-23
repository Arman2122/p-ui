package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/node/agent"
	"github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/state"
	"github.com/Arman2122/p-ui/internal/node/trustedtime"
	"github.com/Arman2122/p-ui/internal/node/worker"
)

func readConfig(path string, result any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return errors.New("node configuration file too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing node configuration data")
	}
	return nil
}

func Agent(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 || args[0] != "run" && args[0] != "enroll" && args[0] != "identity" && args[0] != "recover" && args[0] != "state" && args[0] != "check" {
		return errors.New("usage: penhoon-agent enroll --enrollment FILE | identity | run --runtimes FILE")
	}
	flags := flag.NewFlagSet("penhoon-agent "+args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	dir := flags.String("state-dir", "/var/lib/penhoon-agent", "private durable agent directory")
	configPath := flags.String("config", "/etc/penhoon/node.json", "public node enrollment configuration")
	enrollmentPath := flags.String("enrollment", "", "private enrollment package created by the master")
	runtimesPath := flags.String("runtimes", "/etc/penhoon/runtimes.json", "installed runtime socket registry")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*dir) {
		return errors.New("node state directory must be absolute; unexpected arguments are refused")
	}
	if args[0] != "enroll" && *enrollmentPath != "" {
		return errors.New("an enrollment package is only valid with enroll")
	}
	if runtime.GOOS != "linux" {
		return errors.New("headless nodes require Linux")
	}
	if os.Geteuid() == 0 {
		return errors.New("run penhoon-agent as its dedicated unprivileged service user")
	}
	if args[0] == "check" {
		var endpoints []agent.RuntimeEndpoint
		if err := readConfig(*runtimesPath, &endpoints); err != nil {
			return err
		}
		return agent.CheckRuntimes(ctx, endpoints)
	}
	if args[0] == "enroll" {
		var config credentials.Config
		var ticket string
		if *enrollmentPath != "" {
			pack, err := credentials.ReadEnrollmentPackage(*enrollmentPath)
			if err != nil {
				return err
			}
			config, ticket = pack.Configuration, pack.Ticket
		} else {
			if err := readConfig(*configPath, &config); err != nil {
				return err
			}
			data, err := io.ReadAll(io.LimitReader(input, 128))
			if err != nil {
				return err
			}
			ticket = strings.TrimSpace(string(data))
		}
		vault, err := credentials.Open(filepath.Join(*dir, "credentials.db"), &config)
		if err != nil {
			return err
		}
		defer vault.Close()
		if err := vault.Enroll(ctx, ticket); err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, "Node enrolled. Private credentials are stored locally.")
		return err
	}
	vault, err := credentials.Open(filepath.Join(*dir, "credentials.db"), nil)
	if err != nil {
		return err
	}
	defer vault.Close()
	config := vault.Config()
	if args[0] == "state" {
		_, certificateErr := vault.TLSConfig()
		return json.NewEncoder(output).Encode(struct {
			Enrolled bool               `json:"enrolled"`
			Identity identity.Principal `json:"identity"`
		}{Enrolled: certificateErr == nil, Identity: config.Identity})
	}
	if args[0] == "recover" {
		return vault.Recover(ctx)
	}
	if args[0] == "identity" {
		return json.NewEncoder(output).Encode(config)
	}
	store, err := state.Open(filepath.Join(*dir, "agent.db"), state.Identity{
		NodeID:         config.Identity.NodeID,
		InstallationID: config.Identity.InstallationID, ControlPlaneID: config.Identity.ControlPlaneID,
	})
	if err != nil {
		return err
	}
	defer store.Close()
	var endpoints []agent.RuntimeEndpoint
	if err := readConfig(*runtimesPath, &endpoints); err != nil {
		return err
	}
	local, err := agent.NewLocalRuntimes(store, endpoints)
	if err != nil {
		return err
	}
	defer local.Close()
	configTLS, err := vault.TLSConfig()
	if err != nil {
		return err
	}
	node, err := agent.New(store, config.ControlAddress, configTLS, local, agent.WithCredentials(vault))
	if err != nil {
		return err
	}
	report := func(component string, err error) {
		slog.Warn("node operation failed", "component", component, "error", err)
	}
	done := make(chan struct{})
	go func() { defer close(done); local.Collect(ctx, report) }()
	node.Run(ctx, report)
	<-done
	return nil
}

func Worker(ctx context.Context, args []string, output io.Writer, core string, engine worker.Engine) error {
	return ManagedWorker(ctx, args, output, core, 2*time.Second, func(_ context.Context, dir, id string, store *state.Store, _ credentials.Config) (worker.Backend, io.Closer, error) {
		if persistent, ok := engine.(interface{ SetStateStore(string, *state.Store) }); ok {
			persistent.SetStateStore(dir, store)
		}
		if persistent, ok := engine.(interface{ SetStateDirectory(string) }); ok {
			persistent.SetStateDirectory(dir)
		}
		credit, err := governor.Open(filepath.Join(dir, "governor.db"))
		if err != nil {
			return nil, nil, err
		}
		backend, err := worker.NewStreamBackend(id, engine, store, credit)
		if err != nil {
			_ = credit.Close()
			return nil, nil, err
		}
		return backend, credit, nil
	})
}

type BackendFactory func(context.Context, string, string, *state.Store, credentials.Config) (worker.Backend, io.Closer, error)

func ManagedWorker(ctx context.Context, args []string, output io.Writer, core string, interval time.Duration, factory BackendFactory) error {
	flags := flag.NewFlagSet("penhoon-"+core, flag.ContinueOnError)
	flags.SetOutput(output)
	id := flags.String("id", core, "runtime identity")
	dir := flags.String("state-dir", "/var/lib/penhoon-"+core, "private durable runtime directory")
	configPath := flags.String("config", "/etc/penhoon/node.json", "public node identity configuration")
	socket := flags.String("socket", "/run/penhoon-"+core+"/control.sock", "local control socket")
	agentUID := flags.Uint("agent-uid", 0, "UID permitted to control this runtime")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*dir) || *agentUID == 0 || *agentUID > 1<<32-1 {
		return errors.New("runtime requires an absolute state directory and a non-root agent UID")
	}
	if runtime.GOOS != "linux" {
		return errors.New("headless runtimes require Linux")
	}
	var config credentials.Config
	if err := readConfig(*configPath, &config); err != nil {
		return err
	}
	store, err := state.Open(filepath.Join(*dir, "journal.db"), state.Identity{
		NodeID:         config.Identity.NodeID,
		InstallationID: config.Identity.InstallationID, ControlPlaneID: config.Identity.ControlPlaneID,
	})
	if err != nil {
		return err
	}
	defer store.Close()
	backend, closer, err := factory(ctx, *dir, *id, store, config)
	if err != nil {
		return err
	}
	defer closer.Close()
	process, err := worker.NewWithBackend(ctx, *id, core, store, backend, interval)
	if err != nil {
		return err
	}
	process.SetAssetDirectory(*dir)
	source, err := trustedtime.SystemSource()
	if err != nil {
		return err
	}
	clock, err := trustedtime.New(store, source)
	if err != nil {
		return err
	}
	if err := process.RequireClock(clock); err != nil {
		return err
	}
	listener, err := localrpc.Listen(ctx, *socket, uint32(*agentUID))
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = backend.Shutdown(cleanup)
		return err
	}
	return process.Run(ctx, listener)
}
