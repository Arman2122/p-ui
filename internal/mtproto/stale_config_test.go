package mtproto

import (
	"os"
	"path/filepath"
	"testing"
)

/*
A generated mtg config holds every client secret of its inbound, and every
path that removed one was keyed on the in-memory process map. That map is
empty when the panel starts, so a config for an inbound deleted while the
panel was down stayed on disk indefinitely. F221.

Reconcile now sweeps the directory itself: anything not desired and not kept
goes, whether or not a process was ever attached to it in this run.
*/
func TestReconcileRemovesConfigsForInboundsThatNoLongerExist(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, id := range []int{7, 8} {
		if err := os.WriteFile(configPathForID(id), []byte("[secrets]\n"), 0o600); err != nil {
			t.Fatalf("write config %d: %v", id, err)
		}
	}
	// Not a config of ours: never touched, whatever it is named.
	foreign := filepath.Join(configDir(), "notes.toml")
	if err := os.WriteFile(foreign, []byte("x"), 0o600); err != nil {
		t.Fatalf("write foreign: %v", err)
	}

	mgr := &Manager{procs: map[int]*managed{}, swept: true}
	// Nothing desired; 8 is kept because its caller could not read it.
	if err := mgr.Reconcile(nil, 8); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if _, err := os.Stat(configPathForID(7)); !os.IsNotExist(err) {
		t.Fatalf("config for the deleted inbound 7 still exists (err=%v)", err)
	}
	if _, err := os.Stat(configPathForID(8)); err != nil {
		t.Fatalf("config for the kept inbound 8 was removed: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("a file that is not ours was removed: %v", err)
	}
}
