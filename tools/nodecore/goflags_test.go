package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestXrayModfileReachesChildGoAndPreservesOverlay(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	baseFlags, _, err := moduleGoFlags(root, os.Getenv("GOFLAGS"), "")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "child sources with spaces")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "parent.go")
	child := filepath.Join(dir, "child.go")
	overlaySource := filepath.Join(dir, "overlaid child.go")
	overlayPath := filepath.Join(dir, "source overlay.json")
	files := map[string]string{
		parent: `package main
import("fmt";"os";"os/exec")
func main(){out,err:=exec.Command("go","run",os.Args[len(os.Args)-1]).CombinedOutput();if err!=nil||string(out)!="patched child\n"{fmt.Fprintf(os.Stderr,"child did not inherit pinned module and overlay: %v %s",err,out);os.Exit(1)}}`,
		child: `package main
func main(){panic("inherited overlay was lost")}`,
		overlaySource: `package main
import("fmt";"github.com/xtls/xray-core/common/singbridge")
func main(){if singbridge.PenhoonPatchLevel!=5{panic("patch marker lost")};fmt.Println("patched child")}`,
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {child: overlaySource}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	flags, err := joinGoFlags([]string{"-tags=nodecore_qualification", "-overlay=" + overlayPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", strings.TrimSpace(baseFlags+" "+flags))
	beforeMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	beforeSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runXray(t.Context(), root, xrayOptions{goCommand: "run", args: []string{parent, "--", child}}); err != nil {
		t.Fatal(err)
	}
	afterMod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	afterSum, _ := os.ReadFile(filepath.Join(root, "go.sum"))
	if !bytes.Equal(beforeMod, afterMod) || !bytes.Equal(beforeSum, afterSum) {
		t.Fatal("Xray build modified repository module manifests")
	}
}

func TestXrayTestTrimpathDefaultsAndExplicitOverridesReachBuilds(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	parent, child := filepath.Join(directory, "paths_test.go"), filepath.Join(directory, "child.go")
	if err := os.WriteFile(child, []byte(`package main
import("fmt";"path/filepath";"runtime")
func main(){_,file,_,_:=runtime.Caller(0);fmt.Print(filepath.IsAbs(file))}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte(`package main
import("os";"os/exec";"path/filepath";"runtime";"strconv";"testing")
func TestPaths(t *testing.T){_,file,_,_:=runtime.Caller(0);if strconv.FormatBool(filepath.IsAbs(file))!=os.Getenv("PUI_TEST_PARENT_ABS"){t.Fatalf("parent flag changed: %s",file)};command:=exec.Command("go","run",os.Getenv("PUI_TEST_CHILD_SOURCE"));command.Dir=os.Getenv("PUI_TEST_MODULE_ROOT");output,err:=command.CombinedOutput();if err!=nil||string(output)!=os.Getenv("PUI_TEST_CHILD_ABS"){t.Fatalf("child flag changed: %v %s",err,output)}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, flags, argument, parent, child string }{
		{name: "default", parent: "false", child: "false"},
		{name: "environment false", flags: "-trimpath=false", parent: "true", child: "true"},
		{name: "explicit false", argument: "-trimpath=false", parent: "true", child: "true"},
		{name: "explicit precedence", flags: "-trimpath=false", argument: "-trimpath", parent: "false", child: "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GOFLAGS", test.flags)
			t.Setenv("PUI_TEST_PARENT_ABS", test.parent)
			t.Setenv("PUI_TEST_CHILD_ABS", test.child)
			t.Setenv("PUI_TEST_CHILD_SOURCE", child)
			t.Setenv("PUI_TEST_MODULE_ROOT", root)
			args := []string{"-count=1"}
			if test.argument != "" {
				args = append(args, test.argument)
			}
			args = append(args, parent)
			if err := runXray(t.Context(), root, xrayOptions{goCommand: "test", args: args}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
