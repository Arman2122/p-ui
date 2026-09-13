package xray

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// A tunnel, tun, http or mixed inbound sets no user on its sessions, so a user
// rule on one saved and matched nobody. The mask offers user where it can match.
func TestUserIsOfferedOnlyWhereXrayAuthenticates(t *testing.T) {
	c := New(Deps{})
	for kind, want := range map[core.Kind]bool{"vless": true, "vmess": true, "trojan": true, "shadowsocks": true, "tunnel": false, "tun": false, "http": false, "mixed": false} {
		if got := slices.Contains(c.IngressCriteria(kind), core.CriterionUser); got != want {
			t.Errorf("%s offers user = %v, want %v", kind, got, want)
		}
	}
}
