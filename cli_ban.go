package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// banOptions is what `p-ui ban` was asked to do; one of the actions, or a listing.
type banOptions struct {
	add        string
	minutes    int
	release    string
	email      string
	releaseAll bool
}

/*
runBanCommand lists, adds or releases device-cap bans from the shell, through
the same service the panel's pass uses, so the firewall is converged on the way
out and the running panel agrees with it on its next tick.
*/
func runBanCommand(opts banOptions) error {
	if err := database.InitDB(); err != nil {
		return err
	}
	defer func() { _ = database.CloseDB() }()
	ctx := context.Background()
	now := time.Now()
	svc := &service.PolicyService{}

	switch {
	case opts.add != "":
		row, err := svc.BanAddress(ctx, opts.add, opts.minutes, now)
		if err != nil {
			return err
		}
		fmt.Printf("banned %s on every local inbound port until %s\n", row.Source, time.Unix(row.Until, 0).Format("2006-01-02 15:04:05"))
		return nil
	case opts.release != "":
		var released int
		var err error
		if opts.email != "" {
			released, err = svc.ReleaseBan(ctx, opts.email, opts.release, now)
		} else {
			released, err = svc.ReleaseSource(ctx, opts.release, now)
		}
		if err != nil {
			return err
		}
		if released == 0 {
			return errors.New("no ban held for " + opts.release)
		}
		fmt.Printf("released %d ban(s) of %s\n", released, opts.release)
		return nil
	case opts.releaseAll:
		released, err := svc.ReleaseAllBans(ctx, now)
		if err != nil {
			return err
		}
		fmt.Printf("released %d ban(s)\n", released)
		return nil
	}

	views, err := svc.ActiveBanViews(ctx)
	if err != nil {
		return err
	}
	if len(views) == 0 {
		fmt.Println("no address is banned right now")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CLIENT\tSOURCE\tWHERE\tPORTS\tREASON\tUNTIL")
	for _, v := range views {
		client, where, ports := v.ClientEmail, v.Node, v.Ports
		if client == "" {
			client = "-"
		}
		if where == "" {
			where = "here"
		}
		switch ports {
		case "*":
			ports = "all inbounds"
		case "":
			ports = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", client, v.Source, where, ports, v.Reason, time.Unix(v.Until, 0).Format("2006-01-02 15:04:05"))
	}
	return w.Flush()
}
