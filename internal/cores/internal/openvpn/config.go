package openvpn

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

// paths is where one inbound's daemon keeps its files, under the state dir.
type paths struct {
	dir, conf, cert, key, tc2, plugin, ccd, tmp, mgmt, status string
}

func pathsIn(dir string) paths {
	return paths{
		dir: dir, conf: filepath.Join(dir, "server.conf"), cert: filepath.Join(dir, "server.crt"),
		key: filepath.Join(dir, "server.key"), tc2: filepath.Join(dir, "tc2-server.key"),
		plugin: filepath.Join(dir, "radiusplugin.cnf"), ccd: filepath.Join(dir, "ccd"),
		tmp: filepath.Join(dir, "tmp"), mgmt: filepath.Join(dir, "mgmt.sock"),
		status: filepath.Join(dir, "status.log"),
	}
}

/*
renderServer is the daemon's config. Identity is CA-free: the server's own
certificate doubles as the ca OpenVPN insists on, clients present none, and
the tls-crypt-v2 server key admits only wrapped client keys the panel minted.
Users are authenticated and accounted by the plugin; ccd is where it lands
the tunnel address the sink chose. No user/group here: the supervisor sets
them. DCO stays off: the 2.6 line's counters under it are not to be trusted.
duplicate-cn is on because the device cap is the sink's: without it OpenVPN
disconnects a client's first device the moment its second connects.
*/
func renderServer(inst core.Instance, s Settings, p paths, pluginSO string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "port %d\nproto %s\n", inst.Port, s.Proto)
	if inst.Listen != "" {
		fmt.Fprintf(&b, "local %s\n", inst.Listen)
	}
	fmt.Fprintf(&b, "dev %s\ndev-type tun\ntopology subnet\n", deviceName(inst.ID))
	pool, _ := s.pool()
	fmt.Fprintf(&b, "server %s %s\n", pool.Addr(), s.netmask())
	if s.RedirectGateway {
		b.WriteString("push \"redirect-gateway def1 bypass-dhcp\"\n")
	}
	pushedDNS := 0
	for _, dns := range s.DNS {
		if dns = strings.TrimSpace(dns); dns != "" {
			fmt.Fprintf(&b, "push \"dhcp-option DNS %s\"\n", dns)
			pushedDNS++
		}
	}
	// A Windows client keeps querying its LAN resolver beside the tunnel unless
	// told not to; every other platform ignores a pushed Windows-only option.
	if s.RedirectGateway && pushedDNS > 0 {
		b.WriteString("push \"block-outside-dns\"\n")
	}
	fmt.Fprintf(&b, "ca %s\ncert %s\nkey %s\ndh none\ntls-crypt-v2 %s\n", p.cert, p.cert, p.key, p.tc2)
	b.WriteString("verify-client-cert none\nusername-as-common-name\n")
	fmt.Fprintf(&b, "plugin %s %s\n", pluginSO, p.plugin)
	fmt.Fprintf(&b, "client-config-dir %s\ntmp-dir %s\n", p.ccd, p.tmp)
	// The status file is the plugin's interim byte source and it reads only the
	// version-1 layout; the panel itself never parses it.
	fmt.Fprintf(&b, "management %s unix\nstatus %s 10\n", p.mgmt, p.status)
	b.WriteString("disable-dco\ndata-ciphers AES-256-GCM:CHACHA20-POLY1305\nauth SHA256\ntls-version-min 1.2\n")
	b.WriteString("keepalive 10 60\npersist-key\npersist-tun\nmax-clients 1024\nduplicate-cn\n")
	if s.Proto == ProtoUDP {
		b.WriteString("explicit-exit-notify 1\n")
	}
	// No log file: the daemon logs to stderr, which is the journal under systemd
	// and the supervisor's capture otherwise. A log-append file is never rotated.
	b.WriteString("verb 3\nmute 20\n")
	return b.String()
}

// renderPlugin is openvpn-auth-radius's own config. The port must fit four
// characters: the plugin truncates 18239 to 1823 on the wire.
func renderPlugin(nasID, authAddr, acctAddr, secret string, s Settings, p paths) (string, error) {
	host, authPort, err := net.SplitHostPort(authAddr)
	if err != nil {
		return "", fmt.Errorf("openvpn: radius auth address: %w", err)
	}
	_, acctPort, err := net.SplitHostPort(acctAddr)
	if err != nil {
		return "", fmt.Errorf("openvpn: radius accounting address: %w", err)
	}
	if len(authPort) > 4 || len(acctPort) > 4 {
		return "", fmt.Errorf("openvpn: the RADIUS plugin cannot reach a port above 9999 (%s, %s)", authPort, acctPort)
	}
	return fmt.Sprintf(`NAS-Identifier=%s
Service-Type=5
Framed-Protocol=1
NAS-Port-Type=5
NAS-IP-Address=127.0.0.1
OpenVPNConfig=%s
subnet=%s
overwriteccfiles=true
server
{
	acctport=%s
	authport=%s
	name=%s
	retry=2
	wait=2
	sharedsecret=%s
}
`, nasID, p.conf, s.netmask(), acctPort, authPort, host, secret), nil
}
