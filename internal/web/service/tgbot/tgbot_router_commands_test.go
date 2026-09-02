package tgbot

import (
	"os"
	"strings"
	"testing"
)

/*
The bot could create a client but never delete one, and /restart only knew
Xray. Both are callback and command arms in the router, which no harness
drives end to end; this pins that the arms exist and reach the right
service calls, the way draft_sender_guard_test pins the router's shape.
Feature gaps T2 and T3.
*/
func TestRouterHandlesClientDeleteAndPerCoreRestart(t *testing.T) {
	body, err := os.ReadFile("tgbot_router.go")
	if err != nil {
		t.Fatalf("read tgbot_router.go: %v", err)
	}
	src := string(body)
	for _, want := range []string{
		`case "client_delete":`,
		`case "client_delete_c":`,
		`t.clientService.DeleteByEmail(&t.inboundService, email, false)`,
		`t.serverService.RestartCore(context.Background(), commandArgs[0])`,
		`"tgbot.commands.restartCoreFailed"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("tgbot_router.go lacks %s", want)
		}
	}
	keyboard, err := os.ReadFile("tgbot_client.go")
	if err != nil {
		t.Fatalf("read tgbot_client.go: %v", err)
	}
	if !strings.Contains(string(keyboard), `t.encodeQuery("client_delete "+email)`) {
		t.Error("the client view offers no delete button")
	}
}
