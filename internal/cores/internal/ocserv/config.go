package ocserv

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

// paths is where one inbound's daemon keeps its files, under the state dir.
type paths struct {
	dir, conf, cert, key, radcli, servers, dictionary, ctl, sock, pid, log string
}

func pathsIn(dir string) paths {
	return paths{
		dir: dir, conf: filepath.Join(dir, "ocserv.conf"), cert: filepath.Join(dir, "server.crt"),
		key: filepath.Join(dir, "server.key"), radcli: filepath.Join(dir, "radiusclient.conf"),
		servers: filepath.Join(dir, "servers"), dictionary: filepath.Join(dir, "dictionary"),
		ctl: filepath.Join(dir, "occtl.sock"), sock: filepath.Join(dir, "ocserv.sock"),
		pid: filepath.Join(dir, "ocserv.pid"), log: filepath.Join(dir, "ocserv.log"),
	}
}

/*
renderServer is the daemon's config. Everything about a user comes from the
Access-Accept, which needs groupconfig=true: without it the login succeeds
and the address, DNS, routes and speed limits in the reply are dropped, so
clients silently take pool addresses the panel never chose. There is no
user database on disk, so nothing here changes when a client is added.
*/
func renderServer(inst core.Instance, s Settings, p paths, user string, interimSeconds int) string {
	pool, _ := s.pool()
	var b strings.Builder
	fmt.Fprintf(&b, "auth = \"radius[config=%s,groupconfig=true]\"\nacct = \"radius[config=%s]\"\n", p.radcli, p.radcli)
	if inst.Listen != "" {
		fmt.Fprintf(&b, "listen-host = %s\n", inst.Listen)
	}
	fmt.Fprintf(&b, "tcp-port = %d\nudp-port = %d\n", inst.Port, inst.Port)
	// The installer's core user carries a group of the same name.
	if user != "" {
		fmt.Fprintf(&b, "run-as-user = %s\nrun-as-group = %s\n", user, user)
	}
	fmt.Fprintf(&b, "socket-file = %s\nocctl-socket-file = %s\nuse-occtl = true\n", p.sock, p.ctl)
	fmt.Fprintf(&b, "server-cert = %s\nserver-key = %s\n", p.cert, p.key)
	// The daemon's own default plus a TLS 1.2 floor; DTLS versions are named
	// separately in GnuTLS and are untouched, so dtls-legacy keeps working.
	b.WriteString("tls-priorities = \"NORMAL:%SERVER_PRECEDENCE:%COMPAT:-RSA:-VERS-SSL3.0:-VERS-TLS1.0:-VERS-TLS1.1:-ARCFOUR-128\"\n")
	fmt.Fprintf(&b, "device = %s\nisolate-workers = true\nmax-clients = 0\nmax-same-clients = 0\n", devicePrefixFor(inst.ID))
	fmt.Fprintf(&b, "ipv4-network = %s\nipv4-netmask = %s\npredictable-ips = false\n", pool.Addr(), s.netmask())
	if s.Camouflage {
		fmt.Fprintf(&b, "camouflage = true\ncamouflage_secret = \"%s\"\ncamouflage_realm = \"Restricted Content\"\n", s.CamouflageSecret)
	}
	// Everything below ocserv's own reload marker is re-read on SIGHUP; the
	// panel restarts anyway, so the split matters only to a hand edit.
	b.WriteString("### All configuration options below this line are reloaded on a SIGHUP.\n")
	fmt.Fprintf(&b, "log-level = 2\nstats-report-time = %d\n", interimSeconds)
	b.WriteString("auth-timeout = 240\ncookie-timeout = 60\nidle-timeout = 1200\nmobile-idle-timeout = 2400\n")
	b.WriteString("keepalive = 32400\ndpd = 90\nmobile-dpd = 1800\ntry-mtu-discovery = true\n")
	// Where UDP is throttled or a NAT forgets the mapping, a session whose DTLS
	// went quiet falls back to the TLS connection instead of hanging.
	b.WriteString("switch-to-tcp-timeout = 25\n")
	for _, dns := range s.DNS {
		if dns = strings.TrimSpace(dns); dns != "" {
			fmt.Fprintf(&b, "dns = %s\n", dns)
		}
	}
	if s.RedirectGateway {
		b.WriteString("route = default\ntunnel-all-dns = true\n")
	} else {
		// With no route at all ocserv pushes a default one; the split the form
		// promises is the tunnel subnet alone.
		fmt.Fprintf(&b, "route = %s\n", pool.Masked())
	}
	b.WriteString("cisco-client-compat = true\ndtls-legacy = true\n")
	return b.String()
}

/*
renderRadcli is the RADIUS client half: where to reach the panel, the secret
per server, and a dictionary. The dictionary matters more than it looks --
the distribution ships the vendor files with their includes commented out,
and radcli drops an attribute it cannot name, so without this the speed
limits in an Access-Accept never reach the daemon.
*/
func renderRadcli(nasID, authAddr, acctAddr string, p paths) (string, error) {
	if _, _, err := net.SplitHostPort(authAddr); err != nil {
		return "", fmt.Errorf("ocserv: radius auth address: %w", err)
	}
	if _, _, err := net.SplitHostPort(acctAddr); err != nil {
		return "", fmt.Errorf("ocserv: radius accounting address: %w", err)
	}
	return fmt.Sprintf(`nas-identifier %s
authserver %s
acctserver %s
servers %s
dictionary %s
radius_timeout 5
radius_retries 3
bindaddr *
`, nasID, authAddr, acctAddr, p.servers, p.dictionary), nil
}

const radcliDictionary = `$INCLUDE /etc/radcli/dictionary
$INCLUDE /etc/radcli/dictionary.roaringpenguin
$INCLUDE /etc/radcli/dictionary.microsoft
`

// renderServers is radcli's secret file: one line per server it may talk to.
func renderServers(authAddr, secret string) string {
	host, _, _ := net.SplitHostPort(authAddr)
	return fmt.Sprintf("%s\t%s\n", host, secret)
}
