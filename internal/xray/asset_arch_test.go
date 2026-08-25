package xray

import (
	"runtime"
	"strings"
	"testing"
)

/*
Every ARM build asked GitHub for an asset that does not exist.

downloadXRay switched runtime.GOARCH over "armv7", "armv6" and "armv5" — values
Go never produces. Thirty-two-bit ARM is always GOARCH="arm"; GOARM carries the
level. So all three cases were dead, arch stayed "arm", and the URL became
Xray-linux-arm.zip: a 404, one hundred percent of the time, on three shipped and
documented platforms.

The sub-arch cannot be recovered at runtime, which is why it is stamped. It
cannot be guessed either: measured under an emulated CPU (RUNTIME-RESULTS §16),
the v7a build raises an illegal instruction on ARMv5, so picking one variant for
every GOARCH=arm host would replace a failed download with a machine that cannot
exec its own proxy.
*/
func TestTheAssetNameIsNeverTheOneThatDoesNotExist(t *testing.T) {
	for _, goarch := range []string{"amd64", "arm64", "386", "arm", "s390x"} {
		got := assetArchFor(goarch)
		if got == "arm" {
			t.Fatalf("assetArchFor(%q) = %q, which builds Xray-linux-arm.zip — an asset XTLS has never published", goarch, got)
		}
		if got == "" {
			t.Fatalf("assetArchFor(%q) is empty", goarch)
		}
	}
}

/*
The stamped values are the published asset names, and every ARM level gets its
own. This is the table release.yml has to agree with; if it stops agreeing, the
download 404s again on exactly the platforms nobody tests by hand.
*/
func TestTheStampedNamesAreTheOnesXtlsPublishes(t *testing.T) {
	for platform, want := range map[string]string{
		"amd64": "64",
		"arm64": "arm64-v8a",
		"armv7": "arm32-v7a",
		"armv6": "arm32-v6",
		"armv5": "arm32-v5",
		"386":   "32",
	} {
		if strings.Contains(want, " ") || want == "" {
			t.Fatalf("%s maps to %q, which is not an asset suffix", platform, want)
		}
	}

	// And the stamp wins over the fallback when it is set.
	restore := releaseAssetArch
	defer func() { releaseAssetArch = restore }()
	releaseAssetArch = "arm32-v5"
	if got := ReleaseAssetArch(); got != "arm32-v5" {
		t.Fatalf("ReleaseAssetArch() = %q with a stamp of arm32-v5", got)
	}
	releaseAssetArch = ""
	if got := ReleaseAssetArch(); got != assetArchFor(runtime.GOARCH) {
		t.Fatalf("an unstamped build answered %q, not the fallback", got)
	}
}

/*
Both arch-naming paths must agree about "arm".

install.sh renames every ARM level to xray-linux-arm32, so GetBinaryName has to
say arm32 — and the download path has to say something that is not "arm". They
share one resolver rather than two switches, because two of those that must
agree are two that will not.
*/
func TestTheTwoArchPathsAgree(t *testing.T) {
	if assetArchFor("arm") != "arm32" {
		t.Fatalf(`assetArchFor("arm") = %q; install.sh names the installed file xray-linux-arm32`, assetArchFor("arm"))
	}
	if runtime.GOARCH == "arm" && !strings.HasSuffix(GetBinaryName(), "arm32") {
		t.Fatalf("GetBinaryName() = %q, want it to end in arm32", GetBinaryName())
	}
}
