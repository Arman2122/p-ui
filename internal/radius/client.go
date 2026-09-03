package radius

import (
	"context"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// ProbeLogin sends one PAP Access-Request the way a NAS would and reports
// whether it was accepted: what a conformance rig uses to ask the daemon side
// who is served, since a RADIUS-fed daemon holds no user list of its own.
func ProbeLogin(ctx context.Context, authAddr, secret, nasID, username, password string) (bool, error) {
	p := radius.New(radius.CodeAccessRequest, []byte(secret))
	_ = rfc2865.UserName_SetString(p, username)
	_ = rfc2865.UserPassword_SetString(p, password)
	_ = rfc2865.NASIdentifier_SetString(p, nasID)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := radius.Exchange(ctx, p, authAddr)
	if err != nil {
		return false, err
	}
	return resp.Code == radius.CodeAccessAccept, nil
}
