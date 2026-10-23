//go:build linux

package native

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/vishvananda/netlink"
)

var dynamicPrefix = regexp.MustCompile(`^phvc[1-9][0-9]{0,3}x$`)

func (p *Linux) GuardDynamic(ctx context.Context, prefix string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !dynamicPrefix.MatchString(prefix) {
		return ErrOwnership
	}
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	for _, link := range links {
		if strings.HasPrefix(link.Attrs().Name, prefix) && link.Attrs().Alias != p.owner {
			return ErrOwnership
		}
	}
	for _, hook := range []string{"input", "forward", "output"} {
		data, err := p.run(ctx, "list chain inet "+table+" "+hook)
		if err != nil {
			return err
		}
		var response struct {
			Nftables []struct{ Rule *struct{ Comment string } }
		}
		if err := json.Unmarshal(data, &response); err != nil {
			return err
		}
		found := false
		for _, entry := range response.Nftables {
			if entry.Rule != nil && entry.Rule.Comment == prefix {
				found = true
			}
		}
		if found {
			continue
		}
		var script strings.Builder
		for _, direction := range []string{"iifname", "oifname"} {
			if hook == "input" && direction == "oifname" || hook == "output" && direction == "iifname" {
				continue
			}
			fmt.Fprintf(&script, "insert rule inet %s %s %s %q %s != @managed_ifaces drop comment %q\n", table, hook, direction, prefix+"*", direction, prefix)
		}
		if _, err := p.run(ctx, script.String()); err != nil {
			return err
		}
	}
	return nil
}
