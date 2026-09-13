package service

import "testing"

/*
restartXrayOnClientDisable forced a process replacement on every poll that
switched a client off, to cut the tunnels the depleted clients still held.
When none of them is connected there is no tunnel to cut, and the forced
restart dropped every other client for nothing; the flag now waits for the
next reason in that case. Feature gap A10.
*/
func TestDepletedStillConnectedOnlyWhenADisabledClientIsOnline(t *testing.T) {
	tests := []struct {
		name     string
		disabled []string
		online   []string
		want     bool
	}{
		{name: "one depleted client is online", disabled: []string{"a@x", "b@x"}, online: []string{"c@x", "b@x"}, want: true},
		{name: "depleted clients are all offline", disabled: []string{"a@x", "b@x"}, online: []string{"c@x"}, want: false},
		{name: "nothing was disabled", disabled: nil, online: []string{"a@x"}, want: false},
		{name: "nobody is online", disabled: []string{"a@x"}, online: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientsStillConnected(tc.disabled, tc.online); got != tc.want {
				t.Fatalf("ClientsStillConnected = %v, want %v", got, tc.want)
			}
		})
	}
}
