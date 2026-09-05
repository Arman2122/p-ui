package openvpn

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

/*
An operator's .ovpn, taken under panel control.

A provider's profile is written for a laptop: it names a device, pulls the
server's routes and DNS, and may run scripts. An exit wants none of that. The
panel routes into the tun by policy and nothing else on the host may move, so
the profile is parsed; what the panel decides is dropped, what would run a
command or read a file that is not here is refused, and the rest is kept
verbatim, inline blocks included.
*/
type exitProfile struct {
	kept       []string
	login      [2]string
	wantsLogin bool
	hasRemote  bool
	isClient   bool
	hasPull    bool
}

var (
	errProfileEmpty     = errors.New("openvpn exit: a profile is required (the provider's .ovpn)")
	errProfileNotClient = errors.New("openvpn exit: not a client profile: it has no client directive")
	errProfileNoRemote  = errors.New("openvpn exit: the profile names no remote to dial")
	errProfileNoLogin   = errors.New("openvpn exit: the profile asks for a username and password; enter them")
)

// refusedDirectives run a command on the panel host, or read something that is
// not here. Refused rather than dropped: the operator meant them.
var refusedDirectives = setOf("up", "down", "route-up", "route-pre-down", "ipchange", "tls-verify",
	"client-connect", "client-disconnect", "learn-address", "auth-user-pass-verify", "plugin",
	"script-security", "iproute", "askpass", "config", "chroot", "cd", "tls-export-cert")

// serverDirectives make a profile a server's; an exit needs the client's.
var serverDirectives = setOf("server", "server-ipv6", "server-bridge", "ifconfig-pool",
	"ifconfig-pool-persist", "client-config-dir", "push", "push-reset", "tls-server", "duplicate-cn")

// fileDirectives name a file when given an argument; only their inline form exists here.
var fileDirectives = setOf("ca", "cert", "key", "tls-auth", "tls-crypt", "tls-crypt-v2", "pkcs12",
	"extra-certs", "crl-verify", "dh", "secret")

// droppedDirectives are the panel's to decide: the device, the routes, the
// logging, and everything about how the process lives.
var droppedDirectives = setOf("dev", "dev-type", "dev-node", "nobind", "bind", "lport", "local",
	"persist-tun", "persist-key", "route-nopull", "route", "route-ipv6", "route-gateway", "route-metric",
	"route-delay", "route-noexec", "redirect-gateway", "redirect-private", "dhcp-option",
	"block-outside-dns", "auth-nocache", "log", "log-append", "syslog", "status", "status-version",
	"writepid", "user", "group", "verb", "mute", "inactive", "disable-dco", "ifconfig-noexec",
	"daemon", "inetd", "up-restart", "up-delay")

var inlineBlockOpen = regexp.MustCompile(`^<([a-z0-9-]+)>$`)

func setOf(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func parseExitProfile(raw string) (exitProfile, error) {
	var p exitProfile
	if strings.TrimSpace(raw) == "" {
		return p, errProfileEmpty
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if m := inlineBlockOpen.FindStringSubmatch(line); m != nil {
			end := i + 1
			for end < len(lines) && strings.TrimSpace(lines[end]) != "</"+m[1]+">" {
				end++
			}
			if end >= len(lines) {
				return p, fmt.Errorf("openvpn exit: the <%s> block is never closed", m[1])
			}
			p.block(m[1], lines[i+1:end])
			i = end
			continue
		}
		if err := p.directive(line); err != nil {
			return p, err
		}
	}
	switch {
	case !p.isClient && !p.hasPull:
		return p, errProfileNotClient
	case !p.hasRemote:
		return p, errProfileNoRemote
	}
	return p, nil
}

func (p *exitProfile) block(name string, body []string) {
	if name == "auth-user-pass" {
		p.wantsLogin = true
		var fields []string
		for _, line := range body {
			if s := strings.TrimSpace(line); s != "" {
				fields = append(fields, s)
			}
		}
		if len(fields) >= 2 {
			p.login = [2]string{fields[0], fields[1]}
		}
		return
	}
	// A <connection> block carries its own remote; the rest is kept as written.
	for _, line := range body {
		if fields := strings.Fields(line); len(fields) > 0 && strings.TrimPrefix(fields[0], "--") == "remote" {
			p.hasRemote = true
		}
	}
	p.kept = append(p.kept, "<"+name+">")
	for _, line := range body {
		p.kept = append(p.kept, strings.TrimRight(line, " \t"))
	}
	p.kept = append(p.kept, "</"+name+">")
}

func (p *exitProfile) directive(line string) error {
	fields := strings.Fields(line)
	name := strings.TrimPrefix(fields[0], "--")
	switch {
	case name == "mode" && len(fields) > 1 && fields[1] == "server":
		return fmt.Errorf("openvpn exit: %q makes this a server profile; an exit needs the client's", line)
	case inSet(serverDirectives, name):
		return fmt.Errorf("openvpn exit: %q makes this a server profile; an exit needs the client's", name)
	case inSet(refusedDirectives, name):
		return fmt.Errorf("openvpn exit: %q runs a command on the panel host or reads a file that is not here; remove it from the profile", name)
	case inSet(fileDirectives, name) && len(fields) > 1:
		return fmt.Errorf("openvpn exit: %q names a file; inline it as <%s>…</%s>", line, name, name)
	case name == "auth-user-pass":
		p.wantsLogin = true
		return nil
	case strings.HasPrefix(name, "management"), inSet(droppedDirectives, name):
		return nil
	}
	switch name {
	case "client":
		p.isClient = true
	case "pull", "tls-client":
		p.hasPull = true
	case "remote":
		p.hasRemote = true
	}
	p.kept = append(p.kept, strings.Join(fields, " "))
	return nil
}

func inSet(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}

/*
renderExitConfig is the profile the daemon runs: what the operator wrote, then
the panel's own block last so it wins. route-nopull keeps the pushed address
and drops the pushed routes and DNS; the table the plane fills is the only way
into this device, and nothing on the host is redirected.
*/
func renderExitConfig(id int, p exitProfile, authPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Penhoon UI openvpn exit %d, rendered from the operator's profile. Edit the exit, not this file.\n", id)
	for _, line := range p.kept {
		b.WriteString(line + "\n")
	}
	if !p.isClient {
		b.WriteString("client\n")
	}
	fmt.Fprintf(&b, "dev %s\ndev-type tun\ndisable-dco\nnobind\npersist-tun\npersist-key\nroute-nopull\n", exitDeviceName(id))
	if authPath != "" {
		fmt.Fprintf(&b, "auth-user-pass %s\n", authPath)
	}
	b.WriteString("resolv-retry infinite\nverb 3\nmute 20\n")
	return b.String()
}
