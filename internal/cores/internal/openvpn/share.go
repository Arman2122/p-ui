package openvpn

import (
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
RenderClient is the whole .ovpn, the file every client app imports: the
server pinned by certificate fingerprint (no CA to ship), the client's own
wrapped tls-crypt-v2 key, and its login inline. There is no ovpn:// link
scheme; the subscription serves the file.
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
	if cred.tlsCryptV2 == "" {
		return core.Share{}, fmt.Errorf("openvpn: client %s has no tls-crypt-v2 key yet; save the client once more", user.Email)
	}
	fp, err := fingerprint(settings.ServerCert)
	if err != nil {
		return core.Share{}, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\nclient\ndev tun\nproto %s\nremote %s %d\n", inst.Tag, settings.Proto, host, inst.Port)
	b.WriteString("resolv-retry infinite\nnobind\npersist-key\npersist-tun\nremote-cert-tls server\n")
	fmt.Fprintf(&b, "peer-fingerprint %s\n", fp)
	b.WriteString("data-ciphers AES-256-GCM:CHACHA20-POLY1305\nauth SHA256\nauth-nocache\nverb 3\n")
	if settings.Proto == ProtoUDP {
		b.WriteString("explicit-exit-notify 1\n")
	}
	fmt.Fprintf(&b, "<tls-crypt-v2>\n%s</tls-crypt-v2>\n", strings.TrimRight(cred.tlsCryptV2, "\n")+"\n")
	fmt.Fprintf(&b, "<auth-user-pass>\n%s\n%s\n</auth-user-pass>\n", cred.username, cred.password)
	return core.Share{Kind: "file", Filename: safeName(cred.username) + ".ovpn", Body: b.String()}, nil
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
