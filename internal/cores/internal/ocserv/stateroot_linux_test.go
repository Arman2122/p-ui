//go:build linux

package ocserv

import (
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// The daemon runs as p-ui-core and must traverse the core's own directory under
// /var/lib/p-ui; MkdirAll left it root:root and every daemon died on its config.
func TestTheStateRootIsTraversableByTheDaemonGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chgrp needs root")
	}
	group, err := osuser.LookupGroup("nogroup")
	if err != nil {
		t.Skip("no nogroup on this host")
	}
	gid, _ := strconv.Atoi(group.Gid)
	root := filepath.Join(t.TempDir(), "ocserv")
	c := New(Deps{StateDir: root, Group: gid})
	if err := c.ensureStateRoot(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if st := info.Sys().(*syscall.Stat_t); int(st.Gid) != gid || info.Mode().Perm()&0o050 != 0o050 {
		t.Fatalf("state root is gid %d mode %o; the daemon group cannot traverse it", st.Gid, info.Mode().Perm())
	}
}
