#!/usr/bin/env bash
set -euo pipefail
root=$(pwd -P)
directory=$(realpath -m -- "${1:-$root/scratchpad/node-v2/vm-prerequisites}")
case "$directory" in "$root"/scratchpad/node-v2/*) ;; *) echo 'Guest package cache must remain in the qualification workspace.' >&2; exit 1;; esac
mkdir -p "$directory"
while read -r digest relative; do
  file=${relative##*/}
  if [[ ! -f "$directory/$file" ]]; then
    curl --fail --silent --show-error --max-time 60 --retry 2 "https://archive.ubuntu.com/ubuntu/$relative" -o "$directory/$file.part"
    mv "$directory/$file.part" "$directory/$file"
  fi
  printf '%s  %s\n' "$digest" "$directory/$file" | sha256sum -c -
done <<'PACKAGES'
d648859fccde3c17b8b9b89993427564f91f394a39306db1f67bd5ea3ac42c6e pool/main/n/nftables/nftables_1.0.9-1ubuntu0.1_amd64.deb
fcaee001747a6adaa0cd9337fef437b3aa7a7a6d5246b62fbd4784d3d6a5009e pool/main/n/nftables/libnftables1_1.0.9-1ubuntu0.1_amd64.deb
e5bde5c9cbb95082700011f0b7ef2c0a41900b0a9fd7434f6e90448e2a7b4e50 pool/main/libn/libnftnl/libnftnl11_1.2.6-2build1_amd64.deb
0cf79113f5d193ce9af2be2ff4b2c3b30dd4e55a0b6c47f7d28f6c849ff3aa60 pool/main/j/jansson/libjansson4_2.14-2build2_amd64.deb
PACKAGES
cat > "$directory/SHA256SUMS" <<'SUMS'
d648859fccde3c17b8b9b89993427564f91f394a39306db1f67bd5ea3ac42c6e  nftables_1.0.9-1ubuntu0.1_amd64.deb
fcaee001747a6adaa0cd9337fef437b3aa7a7a6d5246b62fbd4784d3d6a5009e  libnftables1_1.0.9-1ubuntu0.1_amd64.deb
e5bde5c9cbb95082700011f0b7ef2c0a41900b0a9fd7434f6e90448e2a7b4e50  libnftnl11_1.2.6-2build1_amd64.deb
0cf79113f5d193ce9af2be2ff4b2c3b30dd4e55a0b6c47f7d28f6c849ff3aa60  libjansson4_2.14-2build2_amd64.deb
SUMS
