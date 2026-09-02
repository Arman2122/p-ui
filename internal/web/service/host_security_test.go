package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

/*
A host override was accepted whatever the inbound served, so a host declaring
TLS in front of a REALITY inbound, or REALITY in front of a plain one, handed
out links no client could ever complete. Only REALITY is refused: TLS or none
on a TLS or plain inbound is a front terminating for it, which is what hosts
are for. Feature gap H1.
*/
func TestHostSecurityMismatchRefusesOnlyRealityContradictions(t *testing.T) {
	reality := &model.Inbound{Tag: "r", StreamSettings: `{"network":"tcp","security":"reality"}`}
	tls := &model.Inbound{Tag: "t", StreamSettings: `{"network":"ws","security":"tls"}`}
	plain := &model.Inbound{Tag: "p", StreamSettings: `{"network":"ws","security":"none"}`}
	tests := []struct {
		name    string
		host    string
		inbound *model.Inbound
		wantErr string
	}{
		{name: "tls on reality", host: "tls", inbound: reality, wantErr: `host security tls cannot match inbound "r", which serves reality`},
		{name: "none on reality", host: "none", inbound: reality, wantErr: `host security none cannot match inbound "r", which serves reality`},
		{name: "reality on tls", host: "reality", inbound: tls, wantErr: `host security reality cannot match inbound "t", which serves tls`},
		{name: "reality on plain", host: "reality", inbound: plain, wantErr: `host security reality cannot match inbound "p", which serves none`},
		{name: "reality on reality", host: "reality", inbound: reality},
		{name: "tls front for a plain origin", host: "tls", inbound: plain},
		{name: "none in front of tls", host: "none", inbound: tls},
		{name: "same anywhere", host: "same", inbound: reality},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := hostSecurityMismatch(tc.host, tc.inbound)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestAddHostGroupRefusesASecurityTheInboundCannotServe(t *testing.T) {
	setupBulkDB(t)
	svc := &HostService{}
	ib := mkInboundStream(t, 46003, model.VLESS, `{"clients":[]}`, `{"network":"tcp","security":"reality"}`)
	req := &entity.HostGroup{InboundIds: []int{ib.Id}, Remark: "front", Hosts: []string{"cdn.example.com"}, Port: 443, Security: "tls"}
	if _, err := svc.AddHostGroup(req); err == nil || !strings.Contains(err.Error(), "which serves reality") {
		t.Fatalf("tls host on a reality inbound: err = %v, want a refusal naming the inbound", err)
	}
	req.Security = "same"
	if _, err := svc.AddHostGroup(req); err != nil {
		t.Fatalf("same on a reality inbound must be accepted: %v", err)
	}
}
