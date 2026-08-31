#!/usr/bin/env bash
#
# Install, update, remove or report the AmneziaWG kernel module through DKMS.
#
# DKMS rather than a prebuilt .ko because a module is bound to the exact kernel
# it was compiled against: registering the source makes the host rebuild it on
# every kernel upgrade, which is the difference between a tunnel that survives
# `apt upgrade` and one that dies at the next reboot.
#
# Idempotent by design -- the panel calls it on install and on upgrade, and a
# module already built for the running kernel must be left alone.

set -euo pipefail

MODULE_NAME="amneziawg"
# Upstream's own dkms.conf declares this; it is NOT the AmneziaWG release
# version, and changing it here would orphan every previously registered tree.
MODULE_VERSION="1.0.0"
SOURCE_DIR="/usr/src/${MODULE_NAME}-${MODULE_VERSION}"
DEFAULT_VENDORED="/usr/local/p-ui/amneziawg"

red() { echo -e "\033[0;31m$*\033[0m"; }
green() { echo -e "\033[0;32m$*\033[0m"; }
yellow() { echo -e "\033[0;33m$*\033[0m"; }

# secure_boot_blocks reports whether an unsigned module would be refused. A host
# enforcing it needs a key enrolled by a human at the console -- there is no
# unattended path, so the honest move is to say so rather than fail at insmod.
secure_boot_blocks() {
    command -v mokutil >/dev/null 2>&1 || return 1
    mokutil --sb-state 2>/dev/null | grep -qi "SecureBoot enabled"
}

# source_fingerprint identifies the vendored tree by CONTENT. Paths and bytes
# only: cp -a and tar both preserve mtimes, which are not a semantic change.
source_fingerprint() {
    (cd "$1/src" && find . -type f -print0 | LC_ALL=C sort -z |
        xargs -0 sha256sum | sha256sum | cut -d' ' -f1)
}

STAMP_FILE="/var/lib/misc/amneziawg-source.sha256"

# package_family names the installer this host uses. Detected from the tool that
# is actually present rather than from /etc/os-release: a derivative reports its
# own ID while carrying its parent's package manager, and it is the manager we
# need to drive.
package_family() {
    command -v apt-get >/dev/null 2>&1 && { echo apt; return 0; }
    command -v dnf     >/dev/null 2>&1 && { echo dnf; return 0; }
    command -v yum     >/dev/null 2>&1 && { echo yum; return 0; }
    command -v pacman  >/dev/null 2>&1 && { echo pacman; return 0; }
    command -v zypper  >/dev/null 2>&1 && { echo zypper; return 0; }
    command -v apk     >/dev/null 2>&1 && { echo apk; return 0; }
    echo unknown
}

ensure_build_deps() {
    [[ -d "/lib/modules/$(uname -r)/build" ]] && command -v dkms >/dev/null 2>&1         && command -v gcc >/dev/null 2>&1 && return 0

    local family
    family="$(package_family)"
    yellow "Installing the AmneziaWG build prerequisites with ${family}"

    case "$family" in
        apt)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq || true
            # linux-headers-$(uname -r) on Debian/Ubuntu; the generic metapackage
            # is deliberately not used, since it can name a kernel not running.
            apt-get install -y -qq dkms build-essential "linux-headers-$(uname -r)" || true
            ;;
        dnf|yum)
            # kernel-devel is version-matched by uname -r on the RHEL family, and
            # elrepo/EPEL carries dkms. Both are attempted; the header check below
            # is what decides whether it worked.
            "$family" install -y epel-release >/dev/null 2>&1 || true
            "$family" install -y dkms gcc make "kernel-devel-$(uname -r)" || true
            ;;
        pacman)
            # Arch names the headers after the kernel package, not the release:
            # linux-headers for the stock kernel, linux-lts-headers for the LTS one.
            local arch_headers=linux-headers
            [[ "$(uname -r)" == *-lts* ]] && arch_headers=linux-lts-headers
            pacman -Sy --noconfirm --needed dkms base-devel "$arch_headers" || true
            ;;
        zypper)
            zypper --non-interactive install dkms gcc make kernel-devel || true
            ;;
        apk)
            # Alpine is musl and its kernel packages carry no DKMS tree at all.
            # Naming that is more use than a failed install: the module cannot be
            # built here, and no amount of package installation changes it.
            red "Alpine does not ship a DKMS-capable kernel source tree; AmneziaWG cannot be built here."
            return 1
            ;;
        *)
            red "No supported package manager found (looked for apt-get, dnf, yum, pacman, zypper, apk)."
            red "Install dkms, a C toolchain and the headers for $(uname -r) by hand, then re-run this script."
            return 1
            ;;
    esac

    # The header tree is the real test, whatever the package manager reported:
    # every family names its package differently and some exit 0 having installed
    # headers for a kernel this host is not running.
    if [[ ! -d "/lib/modules/$(uname -r)/build" ]]; then
        red "No kernel headers for $(uname -r); AmneziaWG cannot be built here."
        red "Install the header package matching the RUNNING kernel, then: ${pui_folder:-/usr/local/p-ui}/awg-dkms.sh install"
        return 1
    fi
    if ! command -v dkms >/dev/null 2>&1; then
        red "dkms is not available on this host after the install attempt; AmneziaWG cannot be registered."
        return 1
    fi
}

install_module() {
    local vendored="${1:-$DEFAULT_VENDORED}"

    if [[ ! -d "$vendored/src" ]]; then
        red "Vendored AmneziaWG source not found at $vendored"
        return 1
    fi
    if secure_boot_blocks; then
        red "Secure Boot is enabled: the kernel will refuse an unsigned module."
        red "Enrol a signing key with mokutil, or disable Secure Boot. Nothing was installed."
        return 1
    fi
    ensure_build_deps || return 1

    # Built for this kernel AND from this source: dkms status alone is not
    # identity, since MODULE_VERSION is pinned upstream at 1.0.0 forever.
    want_fingerprint="$(source_fingerprint "$vendored")"
    have_fingerprint="$(cat "$STAMP_FILE" 2>/dev/null || true)"
    if dkms status -m "$MODULE_NAME" -v "$MODULE_VERSION" 2>/dev/null | grep -q "$(uname -r)"; then
        if [[ "$want_fingerprint" == "$have_fingerprint" ]]; then
            green "AmneziaWG $MODULE_VERSION already registered for $(uname -r)"
            modprobe "$MODULE_NAME" 2>/dev/null || true
            return 0
        fi
        yellow "The vendored AmneziaWG source changed; rebuilding for $(uname -r)"
    fi

    # Re-register from scratch, so a half-registered tree left by an interrupted
    # upgrade cannot make the build quietly use stale sources.
    dkms remove -m "$MODULE_NAME" -v "$MODULE_VERSION" --all >/dev/null 2>&1 || true
    rm -rf "$SOURCE_DIR"
    mkdir -p "$SOURCE_DIR"
    cp -a "$vendored/src/." "$SOURCE_DIR/"

    yellow "Building AmneziaWG for $(uname -r) -- this takes a minute"
    if ! dkms add -m "$MODULE_NAME" -v "$MODULE_VERSION" >/dev/null 2>&1; then
        red "dkms add failed for ${MODULE_NAME}/${MODULE_VERSION}"
        return 1
    fi
    if ! dkms install -m "$MODULE_NAME" -v "$MODULE_VERSION" >/tmp/awg-dkms-build.log 2>&1; then
        red "AmneziaWG failed to build:"
        tail -20 /tmp/awg-dkms-build.log
        red "Full log: /var/lib/dkms/${MODULE_NAME}/${MODULE_VERSION}/build/"
        return 1
    fi
    if ! modprobe "$MODULE_NAME"; then
        red "AmneziaWG built, but the kernel refused to load it."
        return 1
    fi
    # Loaded at boot, or the panel's inbounds come up before the module does and
    # every device creation fails until something happens to modprobe it.
    echo "$MODULE_NAME" > /etc/modules-load.d/amneziawg.conf

    # Stamped only after a build that loaded, so a failed one is retried.
    mkdir -p "$(dirname "$STAMP_FILE")"
    echo "$want_fingerprint" > "$STAMP_FILE"

    green "AmneziaWG installed and loaded for $(uname -r)"
    warn_if_not_rebuilt_on_kernel_upgrade
}

# warn_if_not_rebuilt_on_kernel_upgrade checks the one property that decides
# whether this survives.
#
# A DKMS module is compiled against the running kernel. On the next kernel
# upgrade the old .ko does not load, and unless DKMS rebuilds it automatically
# the tunnel simply stops existing after a reboot -- with nothing to say why,
# because the panel reports the core unavailable exactly as it would on a host
# that never had it.
#
# `dkms status` naming the module against the running kernel is the evidence
# that registration took. Reported, never fatal: the module IS built and loaded
# by the time this runs, so a host whose automation is unusual still has a
# working tunnel today and now knows what to check after a kernel bump.
warn_if_not_rebuilt_on_kernel_upgrade() {
    if dkms status -m "$MODULE_NAME" -v "$MODULE_VERSION" 2>/dev/null | grep -q "$(uname -r)"; then
        return 0
    fi
    yellow "AmneziaWG is not registered with DKMS for $(uname -r)."
    yellow "It works now, but a kernel upgrade will silently remove it. After the next"
    yellow "kernel change, re-run: ${pui_folder:-/usr/local/p-ui}/awg-dkms.sh install"
}

remove_module() {
    modprobe -r "$MODULE_NAME" 2>/dev/null || true
    rm -f /etc/modules-load.d/amneziawg.conf "$STAMP_FILE"
    dkms remove -m "$MODULE_NAME" -v "$MODULE_VERSION" --all >/dev/null 2>&1 || true
    rm -rf "$SOURCE_DIR"
    green "AmneziaWG removed"
}

status_module() {
    echo "kernel:     $(uname -r)"
    echo "headers:    $([[ -d /lib/modules/$(uname -r)/build ]] && echo present || echo MISSING)"
    echo "dkms:       $(dkms status -m "$MODULE_NAME" 2>/dev/null | tr '\n' ' ' || true)"
    # grep -c, not grep -q: -q exits on the first match, lsmod takes SIGPIPE, and
    # `set -o pipefail` then fails the whole pipeline -- so a module that IS
    # loaded reported "no". Counting reads every line, so nothing is signalled.
    echo "loaded:     $([[ "$(lsmod | grep -c "^${MODULE_NAME}" || true)" != "0" ]] && echo yes || echo no)"
    secure_boot_blocks && echo "secureboot: ENABLED (unsigned modules refused)" || true
}

case "${1:-status}" in
    install) install_module "${2:-}" ;;
    remove)  remove_module ;;
    status)  status_module ;;
    *) echo "usage: $0 {install [source-dir]|remove|status}"; exit 1 ;;
esac
