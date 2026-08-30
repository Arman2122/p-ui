package arch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Every outbound-subscription mutation must ask for a reload.

The subscription's outbounds are merged into the generated config, so enabling
one, disabling it, changing its prepend flag or its order all change what Xray
should be running. Delete, refresh and reorder each called SetToNeedRestart.
Update did not.

So disabling a subscription removed nothing: its outbounds stayed in the running
core and kept serving traffic, indefinitely, because nothing else on the panel
sets that flag on this data. The operator saw the switch off and believed it.

A source guard rather than a behavioural one, because the property is "no
mutation here forgets the signal" -- a test per handler would still miss the
next one added. F354.
*/
func TestEveryOutboundSubscriptionMutationSignalsAReload(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "web", "controller", "xray_setting.go"))
	if err != nil {
		t.Fatalf("read xray_setting.go: %v", err)
	}
	text := string(body)

	mutations := []string{
		"addOutboundSub",
		"updateOutboundSub",
		"deleteOutboundSub",
		"refreshOutboundSub",
	}
	checked := 0
	for _, name := range mutations {
		open := "func (a *XraySettingController) " + name + "(c *gin.Context) {"
		start := strings.Index(text, open)
		if start < 0 {
			continue
		}
		checked++
		rest := text[start+len(open):]
		if end := strings.Index(rest, "\n}"); end >= 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "SetToNeedRestart()") {
			t.Errorf("%s changes what the merged outbounds should be and never asks xray to reload; the running core keeps serving the old set", name)
		}
	}
	if checked == 0 {
		t.Fatal("no outbound-subscription handler matched; this guard is now vacuous")
	}
}
