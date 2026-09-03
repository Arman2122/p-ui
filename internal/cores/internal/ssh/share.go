package ssh

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
RenderClient is the client's whole config in one text file: what to type into
an SSH client, and the payload template the HTTP-Injector family pastes. The
URI is the ssh:// form those apps import; the subscription list carries it so
the client shows up there at all.
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
	port := strconv.Itoa(inst.Port)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — SSH (%s)\n", inst.Tag, transportLabel(settings.Transport))
	fmt.Fprintf(&b, "Host: %s\nPort: %s\nUsername: %s\nPassword: %s\n", host, port, cred.username, cred.password)
	switch settings.Transport {
	case TransportWS:
		fmt.Fprintf(&b, "Transport: WebSocket (HTTP, port %s)\nPath: %s\n", port, pathOrRoot(settings.Path))
		fmt.Fprintf(&b, "Payload: GET %s HTTP/1.1[crlf]Host: %s[crlf]Upgrade: websocket[crlf][crlf]\n", pathOrRoot(settings.Path), host)
	case TransportWSS:
		fmt.Fprintf(&b, "Transport: WebSocket over TLS (port %s)\nSNI: %s\nPath: %s\n", port, host, pathOrRoot(settings.Path))
		fmt.Fprintf(&b, "Payload: GET %s HTTP/1.1[crlf]Host: %s[crlf]Upgrade: websocket[crlf][crlf]\n", pathOrRoot(settings.Path), host)
	default:
		b.WriteString("Transport: plain TCP\n")
	}
	b.WriteString("Forwarding: dynamic (SOCKS) or direct-tcpip; TCP only, no UDP\n")
	return core.Share{
		Kind:     "file",
		Filename: safeName(cred.username) + ".txt",
		Body:     b.String(),
		URI:      shareURI(host, port, cred, settings, inst.Tag),
	}, nil
}

func shareURI(host, port string, cred credential, settings Settings, remark string) string {
	u := url.URL{Scheme: "ssh", User: url.UserPassword(cred.username, cred.password), Host: net.JoinHostPort(host, port), Fragment: remark}
	q := url.Values{}
	if settings.Transport != TransportTCP {
		q.Set("transport", settings.Transport)
		q.Set("path", pathOrRoot(settings.Path))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func transportLabel(t string) string {
	switch t {
	case TransportWS:
		return "WebSocket"
	case TransportWSS:
		return "WebSocket over TLS"
	default:
		return "TCP"
	}
}

func pathOrRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}
