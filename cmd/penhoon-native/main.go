package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
	"github.com/Arman2122/p-ui/internal/node/command"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ocserv-hook" {
		if err := nativeruntime.OcservHook(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "openvpn-learn" {
		if err := nativeruntime.OpenVPNLearn(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := command.ManagedWorker(ctx, os.Args[1:], os.Stdout, "native", 250*time.Millisecond, nativeruntime.Open); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
