package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Arman2122/p-ui/internal/node/credentials"
)

type fixtureState struct {
	Enrollment        credentials.EnrollmentPackage `json:"enrollment"`
	CAKey             []byte                        `json:"caKey"`
	ServerKey         []byte                        `json:"serverKey"`
	ServerCertificate []byte                        `json:"serverCertificate"`
}

func saveJSON(path string, value any, mode os.FileMode) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "prepare" && args[0] != "serve" {
		return errors.New("usage: nodeguest prepare|serve --dir DIR [--bundle DIR]")
	}
	flags := flag.NewFlagSet("nodeguest "+args[0], flag.ContinueOnError)
	dir := flags.String("dir", "", "synthetic guest fixture directory")
	bundle := flags.String("bundle", "", "built Linux node artifacts")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*dir) {
		return errors.New("an absolute isolated fixture directory is required")
	}
	if args[0] == "prepare" {
		if !filepath.IsAbs(*bundle) {
			return errors.New("an absolute built-artifact directory is required")
		}
		if err := os.Mkdir(*dir, 0o700); err != nil {
			return err
		}
		state, err := makeIdentity()
		if err != nil {
			return err
		}
		if err := saveJSON(filepath.Join(*dir, "state.json"), state, 0o600); err != nil {
			return err
		}
		if err := saveJSON(filepath.Join(*dir, "enrollment.json"), state.Enrollment, 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, "ca.crt"), state.Enrollment.Configuration.TrustBundlePEM, 0o644); err != nil {
			return err
		}
		return prepareRepository(*dir, *bundle)
	}
	if _, err := os.Stat("/run/penhoon-vm-qualification"); err != nil {
		if _, err := os.Stat("/run/penhoon-namespace-qualification"); err != nil {
			return errors.New("fixture serving is restricted to the disposable qualification guest")
		}
	}
	data, err := os.ReadFile(filepath.Join(*dir, "state.json"))
	if err != nil {
		return err
	}
	var state fixtureState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	return serve(ctx, *dir, state)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
