//go:build linux

package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

const table = "penhoon_node_v2"

type Linux struct {
	mu              sync.Mutex
	owner           string
	tableOwner      string
	nft             string
	clock           limits.Clock
	network         NetworkBinding
	previousNetwork *NetworkBinding
	durableNetwork  bool
	networkPending  bool
}

func (p *Linux) SetClock(clock limits.Clock) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock = clock
}

func NewLinux(installationID, journalID string) (*Linux, error) {
	if !validID(installationID) || !validID(journalID) {
		return nil, ErrOwnership
	}
	owner := "penhoon-node-v2:" + installationID
	return &Linux{owner: owner, tableOwner: owner + ";journal:" + journalID, nft: "/usr/sbin/nft", network: DefaultNetworkBinding()}, nil
}

func (p *Linux) ConfigureNetwork(state NetworkState) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	target, bound, err := state.Target()
	if err != nil || !bound {
		return ErrAuthority
	}
	p.network, p.durableNetwork, p.networkPending = target, true, state.Pending != nil
	p.previousNetwork = nil
	if state.Pending != nil && state.Current != nil {
		previous := *state.Current
		p.previousNetwork = &previous
	}
	return nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, errors.New("native kernel response exceeds limit")
	}
	return b.Buffer.Write(data)
}

func (p *Linux) run(ctx context.Context, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-f", "-"}
	if strings.HasPrefix(script, "list ") {
		args = append([]string{"-j"}, strings.Fields(script)...)
	}
	cmd := exec.CommandContext(ctx, p.nft, args...)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	cmd.Stdin = strings.NewReader(script)
	out, stderr := &cappedBuffer{limit: 32 << 20}, &cappedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("native nftables: %w: %s", err, stderr.String())
	}
	return out.Bytes(), nil
}

func (p *Linux) Initialize(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	flows, err := p.run(ctx, "list flowtables\n")
	if err != nil {
		return err
	}
	var flowtables struct {
		Nftables []struct{ Flowtable json.RawMessage }
	}
	if err := json.Unmarshal(flows, &flowtables); err != nil {
		return err
	}
	for _, item := range flowtables.Nftables {
		if len(item.Flowtable) != 0 {
			return fmt.Errorf("%w: flow offload bypasses native accounting", ErrAuthority)
		}
	}
	data, err := p.run(ctx, "list tables\n")
	if err != nil {
		return err
	}
	var inventory struct {
		Nftables []struct {
			Table *struct{ Family, Name, Comment string }
		}
	}
	if len(bytes.TrimSpace(data)) != 0 {
		if err := json.Unmarshal(data, &inventory); err != nil {
			return err
		}
	}
	exists := false
	for _, item := range inventory.Nftables {
		if item.Table == nil || item.Table.Family != "inet" || item.Table.Name != table {
			continue
		}
		data, err = p.run(ctx, "list table inet "+table+"\n")
		if err != nil {
			return err
		}
		var existing struct {
			Nftables []struct{ Table *struct{ Comment string } }
		}
		if err := json.Unmarshal(data, &existing); err != nil {
			return err
		}
		for _, item := range existing.Nftables {
			if item.Table != nil && item.Table.Comment == p.tableOwner {
				exists = true
			}
		}
		if !exists {
			return ErrOwnership
		}
	}
	var script strings.Builder
	if exists && p.networkPending {
		counts, err := p.counters(ctx)
		if err != nil {
			return err
		}
		if len(counts) != 0 {
			return ErrBusy
		}
		for _, name := range []string{"up4", "up6", "down4", "down6"} {
			entries, err := p.mappings(ctx, name)
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return ErrBusy
			}
		}
	}
	if !exists {
		if p.durableNetwork {
			if err := p.quarantine(); err != nil {
				return err
			}
		}
		fmt.Fprintf(&script, "create table inet %s { comment %q; }\n", table, p.tableOwner)
	}
	if err := p.initializeDownload(ctx, exists); err != nil {
		return err
	}
	fmt.Fprintf(&script, "add set inet %s managed_ifaces { type ifname; }\n", table)
	for _, version := range []int{4, 6} {
		fmt.Fprintf(&script, "add set inet %s native_dest%d { type ipv%d_addr; }\n", table, version, version)
	}
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	for _, link := range links {
		if deviceName.MatchString(link.Attrs().Name) && link.Attrs().Alias == p.owner && link.Type() != "ifb" {
			fmt.Fprintf(&script, "add element inet %s managed_ifaces { %q }\n", table, link.Attrs().Name)
		}
	}
	fmt.Fprintf(&script, "add chain inet %s download_raw { type filter hook prerouting priority raw; policy accept; }\nflush chain inet %s download_raw\nadd rule inet %s download_raw iifname %q notrack\n", table, table, table, downIn)
	for _, dir := range []string{"up", "down"} {
		for _, version := range []int{4, 6} {
			fmt.Fprintf(&script, "add map inet %s %s%d { type ifname . ipv%d_addr : verdict; flags timeout; }\n", table, dir, version, version)
		}
		fmt.Fprintf(&script, "add chain inet %s %s\n", table, dir)
		fmt.Fprintf(&script, "flush chain inet %s %s\n", table, dir)
		device, address := "iifname", "saddr"
		if dir == "down" {
			device, address = "oifname", "daddr"
		}
		fmt.Fprintf(&script, "add rule inet %s %s %s . ip %s vmap @%s4\n", table, dir, device, address, dir)
		fmt.Fprintf(&script, "add rule inet %s %s %s . ip6 %s vmap @%s6\n", table, dir, device, address, dir)
		fmt.Fprintf(&script, "add rule inet %s %s drop\n", table, dir)
	}
	for _, hook := range []string{"input", "forward", "output"} {
		fmt.Fprintf(&script, "add chain inet %s %s { type filter hook %s priority -10; policy accept; }\n", table, hook, hook)
		fmt.Fprintf(&script, "flush chain inet %s %s\n", table, hook)
		if hook != "output" {
			fmt.Fprintf(&script, "add rule inet %s %s iifname @managed_ifaces jump up\n", table, hook)
		}
		if hook != "input" {
			if hook == "forward" {
				fmt.Fprintf(&script, "add rule inet %s %s iifname %q oifname != @managed_ifaces drop\n", table, hook, downIn)
				fmt.Fprintf(&script, "add rule inet %s %s iifname %q oifname @managed_ifaces jump down\n", table, hook, downIn)
				fmt.Fprintf(&script, "add rule inet %s %s iifname != %q oifname @managed_ifaces drop\n", table, hook, downIn)
			} else {
				fmt.Fprintf(&script, "add rule inet %s %s oifname @managed_ifaces drop\n", table, hook)
			}
			for _, version := range []int{4, 6} {
				family := "ip"
				if version == 6 {
					family = "ip6"
				}
				fmt.Fprintf(&script, "add rule inet %s %s iifname != %q oifname != %q %s daddr @native_dest%d drop\n", table, hook, downIn, downOut, family, version)
			}
		}
	}
	_, err = p.run(ctx, script.String())
	return err
}

func (p *Linux) ownedDevice(name string) (netlink.Link, error) {
	if !deviceName.MatchString(name) {
		return nil, ErrOwnership
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, err
	}
	if link.Attrs().Alias != p.owner {
		return nil, ErrOwnership
	}
	switch link.Type() {
	case "wireguard", "amneziawg", "tun", "tuntap", "veth":
	default:
		return nil, ErrOwnership
	}
	return link, nil
}

// Guard is installed while a new tunnel is still down, before provisioning peers.
func (p *Linux) Guard(ctx context.Context, devices []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(devices) == 0 || len(devices) > 4096 {
		return ErrAuthority
	}
	var script strings.Builder
	guarded, err := p.guarded(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		link, err := p.ownedDevice(device)
		if err != nil {
			return err
		}
		if !guarded[device] && link.Attrs().Flags&net.FlagUp != 0 {
			return ErrOwnership
		}
		fmt.Fprintf(&script, "add element inet %s managed_ifaces { %q }\n", table, device)
	}
	_, err = p.run(ctx, script.String())
	return err
}

func (p *Linux) Install(ctx context.Context, lease Lease) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now, err := authorizationTime(p.clock)
	if err != nil {
		return err
	}
	if err := lease.validate(now); err != nil {
		return err
	}
	all, err := p.counters(ctx)
	if err != nil {
		return err
	}
	if _, exists := all[sliceName(lease.ID)]; exists {
		return ErrBusy
	}
	guarded, err := p.guarded(ctx)
	if err != nil {
		return err
	}
	for _, selector := range lease.Selectors {
		if !guarded[selector.Device] {
			return ErrOwnership
		}
		if _, err := p.ownedDevice(selector.Device); err != nil {
			return err
		}
		if err := p.addDownloadRoute(ctx, selector.Device, netip.PrefixFrom(selector.Address, selector.Address.BitLen())); err != nil {
			return err
		}
	}
	for _, dir := range []string{"up", "down"} {
		for _, version := range []int{4, 6} {
			entries, err := p.mappings(ctx, fmt.Sprintf("%s%d", dir, version))
			if err != nil {
				return err
			}
			for _, selector := range lease.Selectors {
				if entries[selector] != "" {
					return ErrBusy
				}
			}
		}
	}
	if err := p.shape(lease); err != nil {
		return err
	}
	n := sliceName(lease.ID)
	var script strings.Builder
	fmt.Fprintf(&script, "add quota inet %s %s %d bytes\n", table, n, lease.Bytes)
	for i, dir := range []string{"up", "down"} {
		fmt.Fprintf(&script, "add counter inet %s %s_%s\n", table, n, dir)
		fmt.Fprintf(&script, "create chain inet %s %s_%s\n", table, n, dir)
		fmt.Fprintf(&script, "add rule inet %s %s_%s meta time >= %d drop\n", table, n, dir, lease.ExpiresUnixMilli/1000)
		if []limits.Rate{lease.Grant.Policy.Upload, lease.Grant.Policy.Download}[i].Mode != limits.Blocked {
			fmt.Fprintf(&script, "add rule inet %s %s_%s quota name %q counter name %q return\n", table, n, dir, n, n+"_"+dir)
		}
		fmt.Fprintf(&script, "add rule inet %s %s_%s drop\n", table, n, dir)
	}
	now, err = authorizationTime(p.clock)
	if err != nil {
		return err
	}
	remaining := lease.ExpiresUnixMilli - now.UnixMilli()
	if remaining <= 0 {
		return limits.ErrExpired
	}
	for _, selector := range lease.Selectors {
		version := 4
		if selector.Address.Is6() {
			version = 6
		}
		for _, dir := range []string{"up", "down"} {
			fmt.Fprintf(&script, "create element inet %s %s%d { %q . %s timeout %dms : goto %s_%s }\n", table, dir, version, selector.Device, selector.Address, remaining, n, dir)
		}
	}
	_, err = p.run(ctx, script.String())
	return err
}

func (p *Linux) guarded(ctx context.Context) (map[string]bool, error) {
	data, err := p.run(ctx, "list set inet "+table+" managed_ifaces\n")
	if err != nil {
		return nil, err
	}
	var response struct {
		Nftables []struct{ Set *struct{ Elem []string } }
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	result := make(map[string]bool)
	for _, item := range response.Nftables {
		if item.Set != nil {
			for _, device := range item.Set.Elem {
				result[device] = true
			}
		}
	}
	return result, nil
}

func (p *Linux) counters(ctx context.Context) (map[string]Counters, error) {
	data, err := p.run(ctx, "list counters table inet "+table+"\n")
	if err != nil {
		return nil, err
	}
	var response struct {
		Nftables []struct {
			Counter *struct {
				Name  string
				Bytes uint64
			}
		}
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	result := make(map[string]Counters)
	directions := make(map[string]uint8)
	for _, item := range response.Nftables {
		if item.Counter == nil {
			continue
		}
		n, count := item.Counter.Name, item.Counter.Bytes
		if len(n) != 36 && len(n) != 38 {
			return nil, ErrAuthority
		}
		key := n[:33]
		value := result[key]
		switch n[33:] {
		case "_up":
			value.Uploaded = count
			directions[key] |= 1
		case "_down":
			value.Downloaded = count
			directions[key] |= 2
		default:
			return nil, ErrAuthority
		}
		result[key] = value
	}
	for _, seen := range directions {
		if seen != 3 {
			return nil, ErrAuthority
		}
	}
	return result, nil
}

func (p *Linux) mappings(ctx context.Context, name string) (map[Selector]string, error) {
	data, err := p.run(ctx, "list map inet "+table+" "+name+"\n")
	if err != nil {
		return nil, err
	}
	var response struct {
		Nftables []struct {
			Map *struct{ Elem []json.RawMessage }
		}
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	result := make(map[Selector]string)
	for _, item := range response.Nftables {
		if item.Map == nil {
			continue
		}
		for _, raw := range item.Map.Elem {
			key, target, err := mapElement(raw)
			if err != nil {
				return nil, err
			}
			result[key] = target
		}
	}
	return result, nil
}

func (p *Linux) Snapshot(ctx context.Context) (map[string]Counters, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counters(ctx)
}

func (p *Linux) Quarantine(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quarantine()
}

func (p *Linux) quarantine() error {
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	var result error
	for _, link := range links {
		owned := p.OwnsDownloadLink(link)
		if !owned && deviceName.MatchString(link.Attrs().Name) && link.Attrs().Alias == p.owner {
			switch link.Type() {
			case "wireguard", "amneziawg", "tun", "tuntap", "veth":
				owned = true
			}
		}
		if owned {
			result = errors.Join(result, netlink.LinkSetDown(link))
		}
	}
	return result
}

func (p *Linux) Block(ctx context.Context, lease Lease) (Counters, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.block(ctx, lease)
}

func (p *Linux) Stop(ctx context.Context, lease Lease) (Counters, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	count, err := p.block(ctx, lease)
	if err != nil {
		return count, err
	}
	if err := p.unshape(lease); err != nil {
		return count, err
	}
	if err := p.purgeQueues(lease); err != nil {
		return count, err
	}
	return count, nil
}

func (p *Linux) block(ctx context.Context, lease Lease) (Counters, error) {
	if !validID(lease.ID) {
		return Counters{}, ErrAuthority
	}
	all, err := p.counters(ctx)
	if err != nil {
		return Counters{}, err
	}
	n := sliceName(lease.ID)
	if _, ok := all[n]; !ok {
		return Counters{}, ErrMissing
	}
	var script strings.Builder
	for _, dir := range []string{"up", "down"} {
		fmt.Fprintf(&script, "flush chain inet %s %s_%s\nadd rule inet %s %s_%s drop\n", table, n, dir, table, n, dir)
	}
	if _, err := p.run(ctx, script.String()); err != nil {
		return Counters{}, err
	}
	all, err = p.counters(ctx)
	return all[n], err
}

func (p *Linux) Remove(ctx context.Context, lease Lease) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !validID(lease.ID) || validateSelectorList(lease.Selectors, 512) != nil {
		return ErrAuthority
	}
	n := sliceName(lease.ID)
	for attempt := 0; attempt < 3; attempt++ {
		if err := p.unshape(lease); err != nil {
			return err
		}
		var script strings.Builder
		for _, dir := range []string{"up", "down"} {
			for _, version := range []int{4, 6} {
				name := fmt.Sprintf("%s%d", dir, version)
				entries, err := p.mappings(ctx, name)
				if err != nil {
					return err
				}
				for key, target := range entries {
					if target == n+"_"+dir {
						fmt.Fprintf(&script, "delete element inet %s %s { %q . %s }\n", table, name, key.Device, key.Address)
					}
				}
			}
		}
		for _, dir := range []string{"up", "down"} {
			fmt.Fprintf(&script, "flush chain inet %s %s_%s\ndelete chain inet %s %s_%s\ndelete counter inet %s %s_%s\n", table, n, dir, table, n, dir, table, n, dir)
		}
		fmt.Fprintf(&script, "delete quota inet %s %s\n", table, n)
		if _, err := p.run(ctx, script.String()); err == nil {
			return nil
		} else if attempt == 2 {
			return err
		}
	}
	return ErrAuthority
}
