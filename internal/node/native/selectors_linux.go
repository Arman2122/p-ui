//go:build linux

package native

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

func (p *Linux) ReplaceSelectors(ctx context.Context, before, after Lease) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if before.Grant != after.Grant {
		return ErrAuthority
	}
	return p.replaceLease(ctx, before, after, false)
}

func (p *Linux) replaceLease(ctx context.Context, before, after Lease, policy bool) error {
	if !validID(before.ID) || before.ID != after.ID || before.Class != after.Class || before.Bytes != after.Bytes || before.ExpiresUnixMilli != after.ExpiresUnixMilli {
		return ErrAuthority
	}
	now, err := authorizationTime(p.clock)
	if err != nil {
		return err
	}
	if len(after.Selectors) > 0 {
		if err := after.validate(now); err != nil {
			return err
		}
	}
	guarded, err := p.guarded(ctx)
	if err != nil {
		return err
	}
	for _, selector := range after.Selectors {
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
	n := sliceName(before.ID)
	counts, err := p.counters(ctx)
	if err != nil {
		return err
	}
	if _, ok := counts[n]; !ok {
		return ErrMissing
	}
	var replace strings.Builder
	for _, dir := range []string{"up", "down"} {
		for _, version := range []int{4, 6} {
			name := fmt.Sprintf("%s%d", dir, version)
			entries, err := p.mappings(ctx, name)
			if err != nil {
				return err
			}
			for _, selector := range after.Selectors {
				if target := entries[selector]; target != "" && target != n+"_"+dir {
					return ErrBusy
				}
			}
			for selector, target := range entries {
				if target == n+"_"+dir {
					fmt.Fprintf(&replace, "delete element inet %s %s { %q . %s }\n", table, name, selector.Device, selector.Address)
				}
			}
		}
	}
	var block strings.Builder
	for _, dir := range []string{"up", "down"} {
		fmt.Fprintf(&block, "flush chain inet %s %s_%s\nadd rule inet %s %s_%s drop\n", table, n, dir, table, n, dir)
	}
	if _, err := p.run(ctx, block.String()); err != nil {
		return err
	}
	if err := p.unshape(before); err != nil {
		return err
	}
	if policy {
		if err := p.purgeQueues(before); err != nil {
			return err
		}
	}
	if err := p.shapeClass(after, policy, policy); err != nil {
		return err
	}
	now, err = authorizationTime(p.clock)
	if err != nil {
		return err
	}
	remaining := after.ExpiresUnixMilli - now.UnixMilli()
	if len(after.Selectors) > 0 && remaining <= 0 {
		return limits.ErrExpired
	}
	for i, dir := range []string{"up", "down"} {
		fmt.Fprintf(&replace, "flush chain inet %s %s_%s\n", table, n, dir)
		fmt.Fprintf(&replace, "add rule inet %s %s_%s meta time >= %d drop\n", table, n, dir, after.ExpiresUnixMilli/1000)
		if len(after.Selectors) > 0 && []limits.Rate{after.Grant.Policy.Upload, after.Grant.Policy.Download}[i].Mode != limits.Blocked {
			fmt.Fprintf(&replace, "add rule inet %s %s_%s quota name %q counter name %q return\n", table, n, dir, n, n+"_"+dir)
		}
		fmt.Fprintf(&replace, "add rule inet %s %s_%s drop\n", table, n, dir)
	}
	for _, selector := range after.Selectors {
		version := 4
		if selector.Address.Is6() {
			version = 6
		}
		for _, dir := range []string{"up", "down"} {
			fmt.Fprintf(&replace, "create element inet %s %s%d { %q . %s timeout %dms : goto %s_%s }\n", table, dir, version, selector.Device, selector.Address, remaining, n, dir)
		}
	}
	_, err = p.run(ctx, replace.String())
	return err
}
