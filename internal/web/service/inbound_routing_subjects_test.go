package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func seedRoutingInbound(t *testing.T, remark string, port int, protocol model.Protocol, settings string, enable bool, nodeID *int) *model.Inbound {
	t.Helper()
	if settings == "" {
		settings = "{}"
	}
	inbound := &model.Inbound{
		UserId: 1, Remark: remark, Tag: remark, Port: port,
		Protocol: protocol, Settings: settings, Enable: enable, NodeID: nodeID,
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound %s: %v", remark, err)
	}
	return inbound
}

/*
The routing editor offered every tag in the inbounds table, so a rule naming a
node inbound saves cleanly and then never matches a packet.

A wgkernel inbound is ROUTABLE here, and that changed when the routing compile
landed: Xray still never sees its packets, but the compile fronts it and
rewrites the rule onto the front's tag. The question is whether a core declares
a routable ingress, not whether Xray serves the protocol.
*/
func TestRoutingSubjectsMarkUnroutableTags(t *testing.T) {
	initTestDB(t)
	two := 2

	seedRoutingInbound(t, "vless-in", 10443, model.VLESS, "", true, nil)
	seedRoutingInbound(t, "wg-home", 51820, model.Protocol("wgkernel"), `{"address":["10.0.0.1/24"]}`, true, nil)
	seedRoutingInbound(t, "mt-bridged", 8443, model.MTProto, `{"routeThroughXray":true,"routeXrayPort":2398}`, true, nil)
	seedRoutingInbound(t, "mt-plain", 8444, model.MTProto, `{"routeThroughXray":false}`, true, nil)
	seedRoutingInbound(t, "vless-off", 10444, model.VLESS, "", false, nil)
	seedRoutingInbound(t, "vless-node", 10445, model.VLESS, "", true, &two)

	subjects, err := (&InboundService{}).GetRoutingSubjects()
	if err != nil {
		t.Fatalf("GetRoutingSubjects: %v", err)
	}
	got := make(map[string]RoutingSubject, len(subjects))
	for _, s := range subjects {
		got[s.Tag] = s
	}

	for _, tc := range []struct {
		tag       string
		routable  bool
		reasonKey string
	}{
		{"vless-in", true, ""},
		{"mt-bridged", true, ""},
		{"wg-home", true, ""},
		{"mt-plain", false, "pages.xray.subjects.reasonBridgeOff"},
		{"vless-off", false, "pages.xray.subjects.reasonDisabled"},
		{"vless-node", false, "pages.xray.subjects.reasonNode"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			subject, ok := got[tc.tag]
			if !ok {
				t.Fatalf("inbound %s is missing from the subject list", tc.tag)
			}
			if subject.Routable != tc.routable {
				t.Errorf("Routable = %v, want %v", subject.Routable, tc.routable)
			}
			if subject.ReasonKey != tc.reasonKey {
				t.Errorf("ReasonKey = %q, want %q", subject.ReasonKey, tc.reasonKey)
			}
		})
	}
}

/*
The anti-regression: a subject the editor calls routable must be one the compile
can actually realise — either Xray renders it directly, or a core declares an
ingress the compile can front. Changing either side alone turns this red.
*/
func TestRoutingSubjectsAgreeWithTheCores(t *testing.T) {
	initTestDB(t)
	two := 2

	seedRoutingInbound(t, "vless-in", 10443, model.VLESS, "", true, nil)
	seedRoutingInbound(t, "wg-home", 51820, model.Protocol("wgkernel"), `{"address":["10.0.0.1/24"]}`, true, nil)
	seedRoutingInbound(t, "mt-bridged", 8443, model.MTProto, `{"routeThroughXray":true,"routeXrayPort":2398}`, true, nil)
	seedRoutingInbound(t, "vless-node", 10445, model.VLESS, "", true, &two)

	subjects, err := (&InboundService{}).GetRoutingSubjects()
	if err != nil {
		t.Fatalf("GetRoutingSubjects: %v", err)
	}
	var inbounds []*model.Inbound
	if err := database.GetDB().Model(model.Inbound{}).Find(&inbounds).Error; err != nil {
		t.Fatalf("reload inbounds: %v", err)
	}
	byTag := make(map[string]*model.Inbound, len(inbounds))
	for _, inbound := range inbounds {
		byTag[inbound.Tag] = inbound
	}

	for _, subject := range subjects {
		inbound := byTag[subject.Tag]
		handle, err := cores.IngressHandleFor(context.Background(), core.Instance{
			ID: inbound.Id, Kind: core.Kind(inbound.Protocol), Tag: inbound.Tag, Settings: inbound.Settings,
		})
		if err != nil {
			t.Fatalf("IngressHandleFor(%s): %v", subject.Tag, err)
		}
		realisable := inbound.NodeID == nil && handle.BlockedKey == "" &&
			(handle.Tag != "" || handle.Device != "")
		if subject.Routable != realisable {
			t.Errorf("%s: the editor says routable=%v but the cores say %v",
				subject.Tag, subject.Routable, realisable)
		}
	}
}
