package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A JSON subscription must not rewrite the inbound row it was handed.

getConfig wrote each external proxy's dest and port straight onto the
*model.Inbound, so after one client's render the row carried the LAST proxy's
address, and a proxy without a port inherited the previous proxy's. The Clash
renderer works on a copy for exactly this reason. F024.
*/
func TestJsonConfigLeavesTheCallerInboundUntouched(t *testing.T) {
	subReq := NewSubService("")
	js := NewSubJsonService("", "", "", subReq)

	inbound := &model.Inbound{
		Listen:   "203.0.113.21",
		Port:     443,
		Protocol: model.VLESS,
		Remark:   "two-fronts",
		Settings: `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"none",` +
			`"externalProxy":[{"dest":"a.example","port":8080,"remark":"A"},{"dest":"b.example","remark":"B"}]}`,
	}

	configs := js.getConfig(subReq, inbound, model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "user"}, "req.example")
	if len(configs) != 2 {
		t.Fatalf("got %d configs for two external proxies", len(configs))
	}
	if inbound.Listen != "203.0.113.21" || inbound.Port != 443 {
		t.Fatalf("the caller's inbound now reads %s:%d; the render wrote the external proxy onto it", inbound.Listen, inbound.Port)
	}
}
