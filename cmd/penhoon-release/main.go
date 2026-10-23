package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Arman2122/p-ui/internal/node/release"
)

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "init" && args[0] != "fetch" {
		return errors.New("usage: penhoon-release init|fetch")
	}
	flags := flag.NewFlagSet("penhoon-release "+args[0], flag.ContinueOnError)
	dir := flags.String("state-dir", "/usr/local/lib/penhoon/tuf", "protected TUF trust and metadata directory")
	root := flags.String("root", "", "independently authenticated initial root metadata file")
	metadataURL := flags.String("metadata-url", "", "HTTPS repository metadata URL")
	targetsURL := flags.String("targets-url", "", "HTTPS repository target URL")
	platform := flags.String("platform", "", "node target platform")
	sequence := flags.Uint64("sequence", 0, "authorized publisher sequence to fetch")
	output := flags.String("output", "", "new absolute staging directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("release helper requires Linux root and explicit command flags")
	}
	if args[0] == "init" {
		if *sequence != 0 || *output != "" {
			return release.ErrRelease
		}
		file, err := os.Open(*root)
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 512001))
		if err != nil {
			return err
		}
		return release.InitializeRepository(*dir, release.RepositoryConfig{Version: 1, MetadataURL: *metadataURL, TargetsURL: *targetsURL, Platform: *platform}, data)
	}
	if *root != "" || *metadataURL != "" || *targetsURL != "" || *platform != "" {
		return errors.New("fetch uses the installed repository trust")
	}
	repository, err := release.OpenRepository(*dir)
	if err != nil {
		return err
	}
	manifest, err := repository.Fetch(ctx, *sequence, *output)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(manifest)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
