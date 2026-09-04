package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// control is the loopback surface one daemon exposes to the panel: the stats
// API it is billed from and the daemon API its connections are read from.
type control struct {
	statsPort, apiPort int
	secret             string
}

/*
renderConfig is the whole sing-box config for one inbound. Every enabled user
is enumerated under stats.users, because sing-box creates a counter only for a
name it was told about; a user missing there moves bytes nobody bills.
Certificate paths are the panel's own copies (see certPair), readable by the
daemon user, which the operator's originals usually are not.
*/
func renderConfig(inst core.Instance, settings Settings, ctl control, certFile, keyFile string) ([]byte, error) {
	users := make([]map[string]any, 0, len(inst.Users))
	names := make([]string, 0, len(inst.Users))
	for _, u := range inst.Users {
		if !u.Enable {
			continue
		}
		cred, err := credentialOf(inst.Kind, u)
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"name": u.Email, "password": cred.password}
		if inst.Kind == KindTUIC {
			entry["uuid"] = cred.uuid
		}
		users = append(users, entry)
		names = append(names, u.Email)
	}
	sort.Strings(names)
	tls := map[string]any{
		"enabled":          true,
		"certificate_path": certFile,
		"key_path":         keyFile,
	}
	if settings.ServerName != "" {
		tls["server_name"] = settings.ServerName
	}
	inbound := map[string]any{
		"type":        string(inst.Kind),
		"tag":         inst.Tag,
		"listen":      listenAddress(inst.Listen),
		"listen_port": inst.Port,
		"users":       users,
		"tls":         tls,
	}
	switch inst.Kind {
	case KindTUIC:
		tls["alpn"] = []string{"h3"}
		inbound["congestion_control"] = settings.CongestionControl
		inbound["zero_rtt_handshake"] = settings.ZeroRTTHandshake
	case KindAnyTLS:
		if len(settings.PaddingScheme) > 0 {
			inbound["padding_scheme"] = settings.PaddingScheme
		}
	}
	/*
		Where this daemon's own traffic goes. Direct unless the operator asked
		for Xray's router, in which case everything leaves through the loopback
		bridge the panel generates for this inbound's tag, and a routing rule
		can act on it.

		A switch that is on with no bridge port yet is refused rather than
		quietly served direct: that is the window between saving and the port
		being allocated, and going direct there puts every client on the
		server's own address at the moment the operator asked for otherwise.
	*/
	outbounds := []map[string]any{{"type": "direct", "tag": "direct"}}
	final := "direct"
	if settings.RouteThroughXray {
		if settings.RouteXrayPort <= 0 {
			return nil, errors.New("singbox: this inbound routes through Xray and has no bridge port yet; save it again")
		}
		outbounds = append(outbounds, map[string]any{
			"type": "socks", "tag": xrayBridgeTag,
			"server": "127.0.0.1", "server_port": settings.RouteXrayPort,
		})
		final = xrayBridgeTag
	}

	doc := map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": true},
		"experimental": map[string]any{
			"v2ray_api": map[string]any{
				"listen": fmt.Sprintf("127.0.0.1:%d", ctl.statsPort),
				"stats":  map[string]any{"enabled": true, "inbounds": []string{inst.Tag}, "users": names},
			},
		},
		"services": []map[string]any{{
			"type":        "api",
			"tag":         "api",
			"listen":      "127.0.0.1",
			"listen_port": ctl.apiPort,
			"secret":      ctl.secret,
		}},
		"inbounds":  []map[string]any{inbound},
		"outbounds": outbounds,
		"route":     map[string]any{"final": final},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// listenAddress: the panel stores "" for "every address"; sing-box wants it spelled.
func listenAddress(listen string) string {
	if strings.TrimSpace(listen) == "" {
		return "::"
	}
	return listen
}

// checkConfig runs `sing-box check` so a refused config is reported with the
// daemon's own words at save time, not as a unit that fails to start.
func checkConfig(ctx context.Context, binary, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "check", "-c", path).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("singbox: config refused: %s", lastLine(msg))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func newControl() (control, error) {
	stats, err := freeLoopbackPort()
	if err != nil {
		return control{}, err
	}
	api, err := freeLoopbackPort()
	if err != nil {
		return control{}, err
	}
	return control{statsPort: stats, apiPort: api, secret: newSecret()}, nil
}

func freeLoopbackPort() (int, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("singbox: pick a control port: %w", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// readControl recovers the control surface a running daemon was given, from
// the config the panel wrote for it before it lost track.
func readControl(path string) (control, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return control{}, false
	}
	var doc struct {
		Experimental struct {
			V2rayAPI struct {
				Listen string `json:"listen"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
		Services []struct {
			Type       string `json:"type"`
			ListenPort int    `json:"listen_port"`
			Secret     string `json:"secret"`
		} `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return control{}, false
	}
	_, statsPort, err := net.SplitHostPort(doc.Experimental.V2rayAPI.Listen)
	if err != nil {
		return control{}, false
	}
	ctl := control{}
	ctl.statsPort, _ = strconv.Atoi(statsPort)
	for _, svc := range doc.Services {
		if svc.Type == "api" {
			ctl.apiPort, ctl.secret = svc.ListenPort, svc.Secret
		}
	}
	if ctl.statsPort == 0 || ctl.apiPort == 0 || ctl.secret == "" {
		return control{}, false
	}
	return ctl, true
}

// xrayBridgeTag names the outbound that carries this daemon's traffic into the
// panel's loopback bridge. Local to the generated config; the routing subject
// the panel exposes is the inbound's own tag.
const xrayBridgeTag = "xray-bridge"
