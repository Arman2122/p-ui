package entity

import (
	"strings"
	"testing"
)

/*
tgRunTime and ldapSyncCron were saved unparsed; a five-field spec every cron
tutorial gives fell back to the scheduler's default at the next start with
nothing on any screen saying so. The settings validator now runs the same
parser the scheduler does. Feature gap F3.
*/
func TestCheckValidRefusesACronSpecTheSchedulerCannotRun(t *testing.T) {
	tests := []struct {
		name    string
		setting AllSetting
		wantErr string
	}{
		{name: "five-field tgRunTime", setting: AllSetting{WebPort: 2053, SubPort: 2096, TgRunTime: "0 3 * * *"}, wantErr: "tgRunTime:"},
		{name: "garbage ldapSyncCron", setting: AllSetting{WebPort: 2053, SubPort: 2096, LdapSyncCron: "every hour"}, wantErr: "ldapSyncCron:"},
		{name: "six fields", setting: AllSetting{WebPort: 2053, SubPort: 2096, TgRunTime: "0 0 3 * * *", LdapSyncCron: "0 */5 * * * *"}},
		{name: "descriptors", setting: AllSetting{WebPort: 2053, SubPort: 2096, TgRunTime: "@daily", LdapSyncCron: "@every 1m"}},
		{name: "empty means default", setting: AllSetting{WebPort: 2053, SubPort: 2096}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.setting.CheckValid()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want prefix %q", err, tc.wantErr)
			}
		})
	}
}
