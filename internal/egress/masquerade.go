package egress

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

/*
Source NAT for traffic this host FORWARDS out of an L3 inbound.

Forwarding alone is not enough. A packet the kernel forwards keeps the client's
in-tunnel source -- 10.77.0.9 -- and every upstream drops it, so the tunnel
completes a handshake and delivers nothing. Measured on a clean install: with
ip_forward on and no rule, a WireGuard client reaches nothing at all.

This is NOT needed for a routed inbound. A rule sends that traffic through a
front, Xray re-originates it, and the kernel picks the host's own source at
route lookup. It is needed for the plain case, which is also the common one:
somebody makes a WireGuard inbound and expects the internet.

Scoped to the devices the cores name, so it translates this panel's tunnel
traffic and nothing else on the box.
*/

// masqueradeTable is the panel's own nft table. Its own table, in the pattern
// fail2ban already uses here (table inet f2b-table), so applying or deleting it
// whole can never disturb ufw, docker or firewalld: they own different tables.
const masqueradeTable = "p-ui-nat"

// ErrNoNft is a host with no nft binary. Reported, never fatal: the panel still
// routes, it just cannot translate, and preflight says so.
var ErrNoNft = errors.New("egress: nft is not installed, so forwarded traffic cannot be source-translated")

/*
EnsureMasquerade makes the forwarded traffic of these devices leave with the
host's address, and removes the rule entirely when there are none.

The whole table is replaced on every call rather than diffed. It is small, it is
ours alone, and `nft -f` applies it atomically -- so there is no window where
half the devices translate, and no drift for a reconciler to chase.
*/
func EnsureMasquerade(ctx context.Context, devices []string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	nft, err := exec.LookPath("nft")
	if err != nil {
		return ErrNoNft
	}
	if len(devices) == 0 {
		// Idempotent by construction: `destroy` is a no-op on a table that is
		// already gone, where `delete` would be an error to parse and ignore.
		return runNft(ctx, nft, "destroy table inet "+masqueradeTable+"\n")
	}
	return runNft(ctx, nft, masqueradeRuleset(devices))
}

/*
masqueradeRuleset is the whole table, flushed and rewritten in one atomic apply.

`oifname != <dev>` keeps traffic between two of this panel's own tunnels from
being translated: a client reaching another client is not leaving the host, and
rewriting its source would break the return path.
*/
func masqueradeRuleset(devices []string) string {
	sorted := append([]string(nil), devices...)
	sort.Strings(sorted)

	var b strings.Builder
	b.WriteString("table inet " + masqueradeTable + " {\n")
	b.WriteString("\tchain postrouting {\n")
	b.WriteString("\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	for _, device := range sorted {
		fmt.Fprintf(&b, "\t\tiifname %q oifname != %q masquerade comment %q\n",
			device, device, "p-ui: forwarded out of "+device)
	}
	b.WriteString("\t}\n}\n")

	// Flushed first so a device that went away leaves with its rule, and the
	// apply stays atomic: nft -f reads the whole script as one transaction.
	return "table inet " + masqueradeTable + "\nflush table inet " + masqueradeTable + "\n" + b.String()
}

func runNft(ctx context.Context, nft, script string) error {
	cmd := exec.CommandContext(ctx, nft, "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("egress: nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
