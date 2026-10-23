//go:build linux

package inventory

import (
	"os"
	"testing"
)

func TestRunningVersionIdentifiesExecutingInode(t *testing.T) {
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want, err := digestExecutable(t.Context(), file, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RunningVersion(t.Context())
	if err != nil || got != want {
		t.Fatalf("running executable version=%q want=%q err=%v", got, want, err)
	}
}
