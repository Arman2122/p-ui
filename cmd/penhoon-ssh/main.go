package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	sshruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/ssh"
	"github.com/Arman2122/p-ui/internal/node/command"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := command.Worker(ctx, os.Args[1:], os.Stdout, "ssh", &sshruntime.Runtime{}); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
