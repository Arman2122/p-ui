package ocserv

import (
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
RenderClient is what a client needs to connect, as a file, because the
AnyConnect protocol has no link scheme and its clients import no one format:
the official app is typed into by hand, and openconnect reads exactly this
file with --config. So it carries the server, the login and the pin, each on
a line that is also readable by a person copying them into an app.

The pin is only present for a certificate nobody else vouches for, which is
the one the panel minted. An operator who pasted a certificate their clients
already trust gets a profile with nothing to pin.
*/
func (c *Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	settings, err := parseSettings(inst.Settings)
	if err != nil {
		return core.Share{}, err
	}
	cred, err := credentialOf(user)
	if err != nil {
		return core.Share{}, err
	}
	url := fmt.Sprintf("https://%s:%d", hostLabel(host), inst.Port)
	if settings.Camouflage {
		url += "/?" + settings.CamouflageSecret
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n# server   %s\n# username %s\n# password %s\n", inst.Tag, url, cred.username, cred.password)
	b.WriteString("# Import this file with: openconnect --config <this file> ")
	fmt.Fprintf(&b, "%s\n\nprotocol=anyconnect\nuser=%s\n", url, cred.username)
	if settings.ServerCert != "" && selfSigned(settings.ServerCert) {
		pin, err := spkiPin(settings.ServerCert)
		if err != nil {
			return core.Share{}, err
		}
		fmt.Fprintf(&b, "servercert=%s\n", pin)
	}
	return core.Share{Kind: "file", Filename: safeName(cred.username) + ".ocserv.conf", Body: b.String()}, nil
}

// hostLabel wraps a bare IPv6 address in brackets, which a URL needs and a
// hostname must not have.
func hostLabel(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}
