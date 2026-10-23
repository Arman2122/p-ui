#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo 'Usage: bash tools/noderelease/build.sh OUTPUT_DIRECTORY linux-ARCH' >&2
  exit 1
fi
output=$(realpath -m -- "$1")
if [[ -e "$output" || -L "$output" ]]; then
  echo 'Output directory must not already exist.' >&2
  exit 1
fi
case "$2" in
  linux-amd64) arch=amd64; arm= ;;
  linux-arm64) arch=arm64; arm= ;;
  linux-armv7) arch=arm; arm=7 ;;
  linux-armv6) arch=arm; arm=6 ;;
  linux-armv5) arch=arm; arm=5 ;;
  linux-386) arch=386; arm= ;;
  linux-s390x) arch=s390x; arm= ;;
  *) echo 'Unsupported node platform.' >&2; exit 1 ;;
esac
parent=$(dirname -- "$output")
mkdir -p -- "$parent"
work=$(mktemp -d "$parent/.node-build.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
mkdir "$work/release"
host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
env CGO_ENABLED=0 GOOS="$host_os" GOARCH="$host_arch" go build -trimpath -o "$work/nodecore" ./tools/nodecore
for runtime in agent ssh native release; do
  env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" go build -trimpath -o "$work/release/penhoon-$runtime" "./cmd/penhoon-$runtime"
done
for runtime in xray singbox mtproto; do
  env CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" "$work/nodecore" -core "$runtime" -build "$work/release/penhoon-$runtime"
done
mv -T -- "$work/release" "$output"
echo "Built node runtimes and release verifier at $output. Sign only after release qualification."
