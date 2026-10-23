package cores

import (
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/amneziawg"
	"github.com/Arman2122/p-ui/internal/cores/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	"github.com/Arman2122/p-ui/internal/cores/internal/singbox"
	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/cores/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/cores/internal/xray"
)

func NodeRuntimeFor(kind core.Kind) (string, bool) {
	bound, ok := kindOwners().For(kind)
	if !ok {
		return "", false
	}
	switch owner := bound.Core.Describe().ID; owner {
	case wireguard.Kind, amneziawg.Kind, openvpn.ID, ocserv.ID:
		return "native", true
	case xray.ID, ssh.ID, singbox.ID, mtproto.Kind:
		return string(owner), true
	default:
		return "", false
	}
}
