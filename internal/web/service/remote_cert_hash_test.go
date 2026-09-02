package service

import (
	"strings"
	"testing"
)

/*
The certificate-hash probe takes an operator-supplied host and dialled it with
a bare net.Dialer, so it reached anything the panel host can: loopback, the
database, a metadata endpoint, the LAN. The REALITY probe in this same package
dials the identical shape of target through the SSRF guard. F283.
*/
func TestRemoteCertHashRefusesAPrivateTarget(t *testing.T) {
	_, err := (&ServerService{}).GetRemoteCertHash("127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "blocked private/internal address") {
		t.Fatalf("err = %v, want the SSRF guard's refusal, not a dial that reached the panel's own host", err)
	}
}
