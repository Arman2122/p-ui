package egress

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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

Scoped to the devices and the pools the cores name, so it translates this
panel's tunnel traffic and nothing else on the box. A pool is how an inbound
whose sessions each get a transient device names itself: it has no interface to
write, and the source address is what every one of its packets carries.
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
func EnsureMasquerade(ctx context.Context, devices []string, pools []netip.Prefix, fronts []string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	nft, err := exec.LookPath("nft")
	if err != nil {
		return ErrNoNft
	}
	if len(devices) == 0 && len(pools) == 0 {
		return runNft(ctx, nft, masqueradeTeardown())
	}
	return runNft(ctx, nft, masqueradeRuleset(devices, pools, fronts))
}

/*
masqueradeTeardown removes the panel's table without needing `destroy`.

nft only learned `destroy` in 1.0.8. Ubuntu 22.04 ships 1.0.2 and Debian 12
ships 1.0.6, where it is a parse error — measured: exit 1, "syntax error,
unexpected table" — so on two of five supported targets the table was never
torn down. `add` is a no-op on a table that exists, so the `delete` after it
always has a target, on every version.
*/
func masqueradeTeardown() string {
	return "add table inet " + masqueradeTable + "\ndelete table inet " + masqueradeTable + "\n"
}

/*
masqueradeRuleset is the whole table, flushed and rewritten in one atomic apply.

`oifname != <dev>` keeps traffic between two of this panel's own tunnels from
being translated: a client reaching another client is not leaving the host, and
rewriting its source would break the return path.

A front is exempted for a different reason. Traffic leaving on one is read by
Xray and re-originated, so it needs no translation -- and translating it costs
something: postrouting runs before the tun device hands the packet to userspace,
so Xray sees the front's own address and a `source` criterion can never match.
Measured on the VM: with the plain rule the reader sees the gateway address,
without it and with this exemption it sees the client's. F066.
*/
func masqueradeRuleset(devices []string, pools []netip.Prefix, fronts []string) string {
	sorted := append([]string(nil), devices...)
	sort.Strings(sorted)
	exempt := append([]string(nil), fronts...)
	sort.Strings(exempt)
	prefixes := append([]netip.Prefix(nil), pools...)
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })

	var b strings.Builder
	b.WriteString("table inet " + masqueradeTable + " {\n")
	b.WriteString("\tchain postrouting {\n")
	b.WriteString("\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	for _, device := range sorted {
		fmt.Fprintf(&b, "\t\tiifname %q oifname != %q", device, device)
		for _, front := range exempt {
			// One clause per front rather than a negated set: an anonymous set
			// is another thing to be wrong about on nft 1.0.2.
			fmt.Fprintf(&b, " oifname != %q", front)
		}
		fmt.Fprintf(&b, " masquerade comment %q\n", "p-ui: forwarded out of "+device)
	}
	/*
		A pool's rule is the device rule written with the selectors it has. The
		source replaces the ingress device, and `daddr != <pool>` replaces
		`oifname != <dev>`: one client reaching another is not leaving the host,
		and rewriting its source would break the return path just the same.
	*/
	for _, pool := range prefixes {
		if !pool.IsValid() {
			continue
		}
		family := "ip"
		if pool.Addr().Is6() {
			family = "ip6"
		}
		fmt.Fprintf(&b, "\t\t%s saddr %s %s daddr != %s", family, pool, family, pool)
		for _, front := range exempt {
			fmt.Fprintf(&b, " oifname != %q", front)
		}
		fmt.Fprintf(&b, " masquerade comment %q\n", "p-ui: forwarded out of "+pool.String())
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
