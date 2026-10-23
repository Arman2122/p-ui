//go:build linux

package openvpn

import (
	"io"
	"net/netip"
	"os"
	"strings"
	"syscall"
)

func NodeLearn(args []string) error {
	if len(args) < 3 || len(args) > 4 {
		return ErrNodeConfiguration
	}
	if args[1] == "delete" {
		return nil
	}
	if len(args) != 4 || args[1] != "add" && args[1] != "update" || !nodeUsername(args[3]) {
		return ErrNodeConfiguration
	}
	ip, err := netip.ParseAddr(args[2])
	if err != nil || !ip.Is4() {
		return ErrNodeConfiguration
	}
	root, err := os.OpenRoot(args[0])
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(args[3])
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != 0 || stat.Nlink != 1 || info.Mode().Perm()&0o022 != 0 {
		return ErrNodeConfiguration
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return ErrNodeConfiguration
	}
	for _, address := range strings.Fields(string(data)) {
		if address == ip.String() {
			return nil
		}
	}
	return ErrNodeConfiguration
}
