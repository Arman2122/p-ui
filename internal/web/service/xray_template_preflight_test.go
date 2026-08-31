package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

/*
A template the core cannot load is refused at the save, not at the next restart.

CheckXrayConfig builds every OUTBOUND and nothing else, so a routing rule, a dns
block or a policy section xray-core rejects saved as a success -- and the panel
then failed at the next restart, on a config the operator had been told was
fine. Worse on a box where nothing restarts for hours: the template is stored,
every later reconcile refuses, and the reason is a save that reported success.

The `-test` pre-flight is the same load a start performs; it was only ever
reached at restart time. F007.

Needs a real binary, like every other pre-flight test: TestConfig answers nil
when it cannot ask, which is deliberate and would make this vacuous.
*/
func stageXrayBinary(t *testing.T) {
	t.Helper()
	binary := os.Getenv("XRAY_E2E_BINARY")
	if binary == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run the template pre-flight tests")
	}
	dir := t.TempDir()
	staged := filepath.Join(dir, xray.GetBinaryName())
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read %s: %v", binary, err)
	}
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		t.Fatalf("stage the binary: %v", err)
	}
	t.Setenv("PUI_BIN_FOLDER", dir)
}

const loadableTemplate = `{"log":{"loglevel":"warning"},"routing":{"rules":[]},"outbounds":[{"protocol":"freedom","tag":"direct"}]}`

/*
A routing rule with no matcher on it. The core refuses the whole config with
"this rule has no effective fields" -- measured against the pinned binary --
while the outbounds array is perfectly fine, which is all the old check looked
at. An operator writing rules by hand reaches this state easily.
*/
const unloadableTemplate = `{"log":{"loglevel":"warning"},"routing":{"rules":[{"type":"field","outboundTag":"direct","balancerTag":"nope"}]},"outbounds":[{"protocol":"freedom","tag":"direct"}]}`

func TestATemplateTheCoreRejectsIsRefusedAtTheSave(t *testing.T) {
	initTestDB(t)
	stageXrayBinary(t)

	svc := &XraySettingService{}
	if err := svc.SaveXraySetting(loadableTemplate); err != nil {
		t.Fatalf("a loadable template was refused: %v", err)
	}

	err := svc.SaveXraySetting(unloadableTemplate)
	if err == nil {
		t.Fatal("a template the core cannot load was saved as a success; the panel fails at the next restart on a config it said was fine")
	}
	if !strings.Contains(err.Error(), "no effective fields") {
		t.Fatalf("the refusal does not carry the core's own reason: %v", err)
	}
}

/*
And a template that is ALREADY rejected stays editable.

Otherwise the panel refuses to save the repair for the template it refuses to
load, and the operator's only way out is the database. Q1.
*/
func TestAnAlreadyRejectedTemplateCanStillBeEdited(t *testing.T) {
	initTestDB(t)
	stageXrayBinary(t)

	svc := &XraySettingService{}
	// Straight into settings, because the save path is what refuses it.
	if err := svc.saveSetting("xrayTemplateConfig", unloadableTemplate); err != nil {
		t.Fatalf("seed the broken template: %v", err)
	}

	// Still broken, but a different edit: the operator changing the log level
	// on the way to working out what is wrong.
	stillBroken := strings.Replace(unloadableTemplate, `"loglevel":"warning"`, `"loglevel":"debug"`, 1)
	if err := svc.SaveXraySetting(stillBroken); err != nil {
		t.Fatalf("an edit to an already-rejected template was refused, so it can only be repaired in the database: %v", err)
	}

	// And the repair itself lands.
	if err := svc.SaveXraySetting(loadableTemplate); err != nil {
		t.Fatalf("the repair was refused: %v", err)
	}
}
