#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=${SINGBOX_VERSION:?SINGBOX_VERSION is required}
platform=${PLATFORM:?PLATFORM is required}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'A stable vX.Y.Z tag is required'; exit 1; }
baseline=$(sed -n 's/^const Version = "\([^"]*\)"/\1/p' "$root/internal/cores/internal/singbox/version.go")
tags=$(sed -n 's/^const RequiredBuildTags = "\([^"]*\)"/\1/p' "$root/internal/cores/internal/singbox/updates.go")
[[ -n "$baseline" && -n "$tags" && "${version#v}" == "${baseline%.*}".* ]] || { echo 'This release is outside the adapter compatibility line'; exit 1; }
export GOOS=linux CGO_ENABLED=0 GOENV=off GOSUMDB=sum.golang.org GOPROXY=https://proxy.golang.org
unset GONOSUMDB GOPRIVATE GONOPROXY GOBIN
export GOARCH="$platform"
case "$platform" in
  armv5|armv6|armv7) export GOARCH=arm GOARM="${platform#armv}" ;;
  amd64|arm64|386|s390x) unset GOARM ;;
  *) echo 'Unsupported release platform'; exit 1 ;;
esac

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
export GOMODCACHE="$(go env GOMODCACHE)"
export GOPATH="$work/go"
go install -trimpath -tags "$tags" -ldflags "-s -w -X github.com/sagernet/sing-box/constant.Version=${version#v}" "github.com/sagernet/sing-box/cmd/sing-box@$version"
binary=$(find "$GOPATH/bin" -type f -name sing-box -print -quit)
[[ -n "$binary" ]]
pkg="sing-box-${version#v}-linux-${platform}"
mkdir -p "$work/$pkg" "$root/core-release"
cp "$binary" "$work/$pkg/sing-box"
go version -m "$binary" | sed '1s#^.*: #sing-box: #' > "$work/$pkg/build-info.txt"
grep -q 'with_v2ray_api' "$work/$pkg/build-info.txt"
if [[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" == "$GOOS/$GOARCH" ]]; then
  SINGBOX_UPDATE_E2E_BINARY="$binary" SINGBOX_UPDATE_E2E_VERSION="$version" go test "$root/internal/cores/internal/singbox" -run '^TestPublishedUpdateCandidate$' -count=1
fi
tar -czf "$root/core-release/$pkg.tar.gz" -C "$work" "$pkg"
(cd "$root/core-release" && sha256sum "$pkg.tar.gz" > "$pkg.tar.gz.sha256")
