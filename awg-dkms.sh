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

ensure_build_deps() {
    local missing=()
    command -v dkms >/dev/null 2>&1 || missing+=(dkms)
    command -v gcc >/dev/null 2>&1 || missing+=(build-essential)
    [[ -d "/lib/modules/$(uname -r)/build" ]] || missing+=("linux-headers-$(uname -r)")
    [[ ${#missing[@]} -eq 0 ]] && return 0

    yellow "Installing build prerequisites: ${missing[*]}"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq || true
    # Not fatal on its own: the header check below names the real problem, and a
    # host whose running kernel has no matching headers package is worth naming.
    apt-get install -y -qq "${missing[@]}" || true

    if [[ ! -d "/lib/modules/$(uname -r)/build" ]]; then
        red "No kernel headers for $(uname -r); AmneziaWG cannot be built here."
        red "Install the matching linux-headers package, then: p-ui awg install"
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

    # Already built for THIS kernel: leave it alone. dkms status is the only
    # honest source -- a loaded module says nothing about the next kernel.
    if dkms status -m "$MODULE_NAME" -v "$MODULE_VERSION" 2>/dev/null | grep -q "$(uname -r)"; then
        green "AmneziaWG $MODULE_VERSION already registered for $(uname -r)"
        modprobe "$MODULE_NAME" 2>/dev/null || true
        return 0
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

    green "AmneziaWG installed and loaded for $(uname -r)"
}

remove_module() {
    modprobe -r "$MODULE_NAME" 2>/dev/null || true
    rm -f /etc/modules-load.d/amneziawg.conf
    dkms remove -m "$MODULE_NAME" -v "$MODULE_VERSION" --all >/dev/null 2>&1 || true
    rm -rf "$SOURCE_DIR"
    green "AmneziaWG removed"
}

status_module() {
    echo "kernel:     $(uname -r)"
    echo "headers:    $([[ -d /lib/modules/$(uname -r)/build ]] && echo present || echo MISSING)"
    echo "dkms:       $(dkms status -m "$MODULE_NAME" 2>/dev/null | tr '\n' ' ' || true)"
    echo "loaded:     $(lsmod | grep -q "^${MODULE_NAME}" && echo yes || echo no)"
    secure_boot_blocks && echo "secureboot: ENABLED (unsigned modules refused)" || true
}

case "${1:-status}" in
    install) install_module "${2:-}" ;;
    remove)  remove_module ;;
    status)  status_module ;;
    *) echo "usage: $0 {install [source-dir]|remove|status}"; exit 1 ;;
esac
