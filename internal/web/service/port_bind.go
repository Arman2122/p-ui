package service

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type bindAddr struct {
	listen string
	v6only bool
}

func inboundBindAddr(inbound *model.Inbound) bindAddr {
	address := bindAddr{listen: inbound.Listen}
	if !cores.ServedByXray(core.Kind(inbound.Protocol)) {
		return address
	}
	var stream struct {
		Sockopt struct {
			V6Only bool `json:"v6only"`
		} `json:"sockopt"`
	}
	if json.Unmarshal([]byte(inbound.StreamSettings), &stream) == nil {
		address.v6only = stream.Sockopt.V6Only
	}
	return address
}

func bindListenOverlaps(a, b bindAddr) bool {
	return core.BindListenOverlaps(
		core.BindAddress{Listen: a.listen, V6Only: a.v6only},
		core.BindAddress{Listen: b.listen, V6Only: b.v6only},
	)
}
