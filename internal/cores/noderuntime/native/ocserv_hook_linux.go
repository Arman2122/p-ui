//go:build linux

package nativeruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vishvananda/netlink"

	"github.com/Arman2122/p-ui/internal/node/localrpc"
	"github.com/Arman2122/p-ui/internal/node/native"
)

type (
	ocservHook struct {
		Generation, Reason, Device, Username string
		PID                                  int
		Address                              netip.Addr
	}
	ocservBinding struct {
		Generation, Username, ClientID, Device string
		PID, Index                             int
		Address                                netip.Addr
	}
)

func httpHandler(r *Runtime) http.Handler { return http.HandlerFunc(r.serveOcservHook) }

func OcservHook(args []string) error {
	if len(args) != 2 || !filepath.IsAbs(args[0]) {
		return errors.New("invalid ocserv hook arguments")
	}
	pid, err := strconv.Atoi(os.Getenv("ID"))
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(os.Getenv("IP_REMOTE"))
	if err != nil {
		return err
	}
	request := ocservHook{Generation: args[1], Reason: os.Getenv("REASON"), Device: os.Getenv("DEVICE"), Username: os.Getenv("USERNAME"), PID: pid, Address: ip}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	client, err := localrpc.Client(args[0], 0)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	client.Timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://native/session", bytes.NewReader(data))
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("ocserv session authorization rejected")
	}
	return nil
}

func (r *Runtime) serveOcservHook(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/session" {
		http.NotFound(w, request)
		return
	}
	var event ocservHook
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&event) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ocservEvent(request.Context(), event); err != nil {
		if event.Reason == "connect" {
			slog.Warn("ocserv session rejected", "device", event.Device, "error", err)
		}
		http.Error(w, "session rejected", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Runtime) ocservEvent(ctx context.Context, event ocservHook) error {
	if !r.configured || event.PID <= 0 || event.Reason != "connect" && event.Reason != "disconnect" {
		return native.ErrAuthority
	}
	process := r.ocserv.byGeneration(event.Generation)
	if process == nil {
		return native.ErrAuthority
	}
	if !strings.HasPrefix(event.Device, process.config.Prefix()) {
		return native.ErrOwnership
	}
	client, ok := process.clients[event.Username]
	if !ok || !client.Permits(event.Address) {
		return native.ErrAuthority
	}
	if event.Reason == "disconnect" {
		current, ok := r.ocserv.bindings[event.Device]
		if ok && current.Generation == event.Generation && current.Username == event.Username && current.PID == event.PID {
			delete(r.ocserv.bindings, event.Device)
		}
		return r.applyNativeAssignments(ctx)
	}
	if !process.auth.AuthenticationCurrent(event.Username) {
		return native.ErrAuthority
	}
	grant, ok := r.grant(client.ClientID)
	now, err := r.authorizationTime()
	if err != nil || !ok || !grant.Policy.Enabled || grant.Policy.ExpiresUnixMilli != 0 && grant.Policy.ExpiresUnixMilli <= now.UnixMilli() {
		return native.ErrAuthority
	}
	status, err := os.ReadFile("/proc/" + strconv.Itoa(event.PID) + "/status")
	if err != nil {
		return err
	}
	uid, parent, gid, groups, caps, ambient := "", "", "", "", "", ""
	for line := range strings.SplitSeq(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			uid = strings.TrimSpace(strings.TrimPrefix(line, "Uid:"))
		}
		if strings.HasPrefix(line, "PPid:") {
			parent = strings.TrimSpace(strings.TrimPrefix(line, "PPid:"))
		}
		for name, target := range map[string]*string{"Gid:": &gid, "Groups:": &groups, "CapEff:": &caps, "CapAmb:": &ambient} {
			if strings.HasPrefix(line, name) {
				*target = strings.TrimSpace(strings.TrimPrefix(line, name))
			}
		}
	}
	if parent != strconv.Itoa(process.command.Process.Pid) || len(strings.Fields(uid)) != 4 {
		return native.ErrOwnership
	}
	for _, value := range strings.Fields(uid) {
		if value != strconv.FormatUint(uint64(r.ocserv.uid), 10) {
			return native.ErrOwnership
		}
	}
	wantedGroup := strconv.FormatUint(uint64(r.ocserv.gid), 10)
	if len(strings.Fields(gid)) != 4 || groups != wantedGroup || caps != "0000000000000000" || ambient != "0000000000000000" {
		return native.ErrOwnership
	}
	for _, value := range strings.Fields(gid) {
		if value != wantedGroup {
			return native.ErrOwnership
		}
	}
	link, err := netlink.LinkByName(event.Device)
	if err != nil {
		return err
	}
	tun, ok := link.(*netlink.Tuntap)
	if !ok || tun.Mode != netlink.TUNTAP_MODE_TUN || tun.Owner != r.ocserv.uid || tun.Group != r.ocserv.gid || tun.Attrs().Alias != "" && tun.Attrs().Alias != r.ocserv.plane.owner {
		return native.ErrOwnership
	}
	if err := netlink.LinkSetDown(link); err != nil {
		return err
	}
	if err := netlink.LinkSetAlias(link, r.ocserv.plane.owner); err != nil {
		return err
	}
	if err := r.ocserv.plane.gate.Guard(ctx, []string{event.Device}); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}
	for device, binding := range r.ocserv.bindings {
		if binding.Address == event.Address {
			if binding.ClientID != client.ClientID {
				return native.ErrAuthority
			}
			delete(r.ocserv.bindings, device)
		}
	}
	r.ocserv.bindings[event.Device] = ocservBinding{Generation: event.Generation, Username: event.Username, ClientID: client.ClientID, Device: event.Device, PID: event.PID, Index: link.Attrs().Index, Address: event.Address}
	if err := r.applyNativeAssignments(ctx); err != nil {
		return err
	}
	return nil
}
