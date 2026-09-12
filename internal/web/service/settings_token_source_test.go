package service

import (
	"strconv"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
)

func TestAPITokenSourceChecksPersistedNetworksAndFailsClosed(t *testing.T) {
	initTestDB(t)
	const bearer = "source-restriction-test"
	token := model.ApiToken{Name: "restricted", Token: crypto.HashTokenSHA256(bearer), Enabled: true, Scope: "read"}
	if err := database.GetDB().Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	value, err := NormalizeTokenSourceNetworks("192.0.2.9, 2001:db8:1234::1/48")
	if err != nil {
		t.Fatal(err)
	}
	if value != "192.0.2.9/32,2001:db8:1234::/48" {
		t.Fatalf("networks = %q", value)
	}
	key := "apiTokenAllowedIPs:" + strconv.Itoa(token.Id)
	if err := (&SettingService{}).saveSetting(key, value); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"192.0.2.9", "::ffff:192.0.2.9", "2001:db8:1234:abcd::1"} {
		if err := CheckAPITokenSource(bearer, source); err != nil {
			t.Fatalf("allowed source %s: %v", source, err)
		}
	}
	if err := CheckAPITokenSource(bearer, "192.0.2.10"); err == nil || err.Error() != "token is not permitted from this source IP" {
		t.Fatalf("outside source = %v", err)
	}
	if err := (&SettingService{}).saveSetting(key, "invalid-network"); err != nil {
		t.Fatal(err)
	}
	if err := CheckAPITokenSource(bearer, "192.0.2.9"); err == nil || err.Error() != `invalid token source IP or CIDR: "invalid-network"` {
		t.Fatalf("corrupt policy = %v", err)
	}
}
