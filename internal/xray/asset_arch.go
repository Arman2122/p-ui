package xray

import "runtime"

/*
releaseAssetArch is the XTLS asset suffix for the platform this panel was built
for, stamped at link time by release.yml.

GOARCH alone cannot answer it. Thirty-two-bit ARM is always GOARCH="arm" — GOARM
carries 5, 6 or 7 — so the download path's switch over "armv7"/"armv6"/"armv5"
matched nothing and every ARM build asked GitHub for Xray-linux-arm.zip, an
asset XTLS does not publish. release.yml already has the real sub-arch in
matrix.platform and already builds an LDFLAGS string, so stamping it is an
append rather than new plumbing.

Guessing is not available as a fallback: measured under an emulated CPU
(RUNTIME-RESULTS §16), a v7a binary raises SIGILL on ARMv5, so shipping one
variant to every GOARCH=arm host would brick armv5 and armv6 boxes at the first
exec — worse than the 404, which at least leaves the working binary alone.
*/
var releaseAssetArch string

// ReleaseAssetArch is the asset suffix, or the best answer an unstamped build
// can give. Both this and GetBinaryName resolve arm the same way on purpose:
// two arch-naming paths that must agree are two paths that will not.
func ReleaseAssetArch() string {
	if releaseAssetArch != "" {
		return releaseAssetArch
	}
	return assetArchFor(runtime.GOARCH)
}

/*
assetArchFor is the unstamped fallback, and it deliberately refuses to guess an
ARM sub-arch. "arm32" is not a published asset either, so an unstamped ARM build
still fails to download — loudly, and without installing a binary the board
cannot execute. That is the same answer GetBinaryName gives for the installed
file's name, so a dev build names one thing consistently.
*/
func assetArchFor(goarch string) string {
	switch goarch {
	case "amd64":
		return "64"
	case "arm64":
		return "arm64-v8a"
	case "386":
		return "32"
	case "arm":
		return "arm32"
	default:
		return goarch
	}
}
