#!/usr/bin/env bash
#
# smoke-noninteractive.sh — verify the Penhoon UI non-interactive install path.
#
# Runs install.sh inside an Ubuntu container with NO TTY (piped) and
# PUI_NONINTERACTIVE=1, then asserts:
#   * /etc/p-ui/install-result.env exists (mode 600) with random, non-default creds
#   * the panel reports hasDefaultCredential: false (no admin/admin remains)
#   * the panel HTTP server actually serves on the generated port/base path
#   * with a [version] argument: the installed binary reports exactly that version
#
# Requires Docker and network access (install.sh downloads the released binary).
# Usage: bash deploy/test/smoke-noninteractive.sh [version]
#   With no argument install.sh resolves releases/latest. Pass an explicit tag
#   (e.g. v3.4.2) to verify that exact release — the tag-triggered CI run does
#   this so it cannot silently validate the previous release
#   (upstream MHSanaei/3x-ui#5756).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IMAGE="${SMOKE_IMAGE:-ubuntu:24.04}"
PUI_SMOKE_VERSION="${1:-}"

if ! command -v docker > /dev/null 2>&1; then
    echo "ERROR: docker is required for this smoke test." >&2
    exit 1
fi

echo "== non-interactive install smoke test (image: $IMAGE, version: ${PUI_SMOKE_VERSION:-latest}) =="

docker run --rm \
    -v "${REPO_ROOT}/install.sh:/root/install.sh:ro" \
    -e PUI_NONINTERACTIVE=1 \
    -e PUI_SSL_MODE=none \
    -e PUI_SMOKE_VERSION="$PUI_SMOKE_VERSION" \
    -e DEBIAN_FRONTEND=noninteractive \
    "$IMAGE" bash -euo pipefail -c '
        apt-get update -qq
        apt-get install -y -qq curl tar openssl ca-certificates > /dev/null

        echo "--- running install.sh piped (no TTY), version: ${PUI_SMOKE_VERSION:-latest} ---"
        # Piping guarantees stdin is not a TTY, exercising the auto non-interactive path.
        if [ -n "${PUI_SMOKE_VERSION:-}" ]; then
            cat /root/install.sh | bash -s -- "$PUI_SMOKE_VERSION"
        else
            cat /root/install.sh | bash
        fi

        echo "--- assertions ---"
        if [ -n "${PUI_SMOKE_VERSION:-}" ]; then
            installed=$(/usr/local/p-ui/p-ui -v)
            [ "$installed" = "${PUI_SMOKE_VERSION#v}" ] \
                || { echo "FAIL: installed version $installed, want ${PUI_SMOKE_VERSION#v}"; exit 1; }
        fi

        RESULT=/etc/p-ui/install-result.env
        test -f "$RESULT" || { echo "FAIL: $RESULT missing"; exit 1; }

        perms=$(stat -c %a "$RESULT")
        [ "$perms" = "600" ] || { echo "FAIL: $RESULT perms=$perms (want 600)"; exit 1; }

        # shellcheck disable=SC1090
        . "$RESULT"
        [ -n "${PUI_USERNAME:-}" ] && [ "$PUI_USERNAME" != "admin" ] \
            || { echo "FAIL: username missing or still admin"; exit 1; }
        [ -n "${PUI_PASSWORD:-}" ] && [ "$PUI_PASSWORD" != "admin" ] \
            || { echo "FAIL: password missing or still admin"; exit 1; }
        [ -n "${PUI_PANEL_PORT:-}" ] || { echo "FAIL: port missing"; exit 1; }

        # No default admin in the DB.
        /usr/local/p-ui/p-ui setting -show | grep -q "hasDefaultCredential: false" \
            || { echo "FAIL: hasDefaultCredential is not false"; exit 1; }

        echo "--- verifying the panel serves HTTP ---"
        cd /usr/local/p-ui
        ./p-ui > /tmp/pui.log 2>&1 &
        pui_pid=$!
        for _ in $(seq 1 15); do
            code=$(curl -s -o /dev/null -w "%{http_code}" \
                "http://127.0.0.1:${PUI_PANEL_PORT}/${PUI_WEB_BASE_PATH}/" 2>/dev/null || true)
            case "$code" in 200|301|302|307|308) break ;; esac
            sleep 1
        done
        kill "$pui_pid" 2>/dev/null || true
        echo "panel HTTP status: ${code:-none}"
        case "${code:-}" in
            200|301|302|307|308) : ;;
            *) echo "FAIL: panel did not serve (status ${code:-none})"; tail -n 30 /tmp/pui.log; exit 1 ;;
        esac

        echo "SMOKE_PASS: user=$PUI_USERNAME port=$PUI_PANEL_PORT path=$PUI_WEB_BASE_PATH"
    '

echo "== non-interactive smoke test PASSED =="
