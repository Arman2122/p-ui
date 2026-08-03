#!/usr/bin/env bash
#
# smoke-noninteractive.sh — verify the Penhoon UI non-interactive install path.
#
# Runs install.sh on THIS machine with NO TTY (piped) and PUI_NONINTERACTIVE=1,
# then asserts:
#   * /etc/p-ui/install-result.env exists (mode 600) with random, non-default creds
#   * /etc/default/p-ui exists (mode 600) and pins a postgres:// PUI_DB_DSN —
#     PostgreSQL is the only backend, so the DSN is mandatory
#   * PUI_DB_TYPE is written nowhere and no SQLite artefact is left behind
#   * pg_dump is installed (the panel's backup/restore is pg_dump/pg_restore only)
#   * the panel reports hasDefaultCredential: false (no admin/admin remains)
#   * the p-ui systemd unit is active and serves the generated port/base path
#   * with a [version] argument: the installed binary reports exactly that version
#
# The install is NOT sandboxed: it installs the panel and a local PostgreSQL onto
# the machine that runs it (install.sh requires apt-get, systemd and root, so
# there is nothing to isolate it with). Use a throwaway Ubuntu/Debian VM or an
# ephemeral CI runner; outside CI you must opt in with PUI_SMOKE_ALLOW_HOST=1.
#
# Requires Ubuntu 22.04+ / Debian 12+, systemd, root (the script re-execs itself
# through sudo) and network access (install.sh downloads the released binary and
# apt-installs PostgreSQL).
#
# Usage: bash deploy/test/smoke-noninteractive.sh [version]
#   With no argument install.sh resolves releases/latest. Pass an explicit tag
#   (e.g. v1.0.0) to verify that exact release — the tag-triggered CI run does
#   this so it cannot silently validate the previous release
#   (upstream MHSanaei/3x-ui#5756).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PUI_SMOKE_VERSION="${1:-}"

RESULT_FILE=/etc/p-ui/install-result.env
# The systemd unit ships EnvironmentFile=-/etc/default/p-ui, so that is where the
# installer pins the PostgreSQL DSN the panel needs to start at all.
ENV_FILE=/etc/default/p-ui

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

# --- guards -----------------------------------------------------------------
if [ "${CI:-}" != "true" ] && [ "${PUI_SMOKE_ALLOW_HOST:-}" != "1" ]; then
    cat >&2 << 'EOF'
ERROR: this smoke test installs Penhoon UI and a local PostgreSQL onto the
       machine it runs on — it does not sandbox anything. Run it on a throwaway
       Ubuntu/Debian VM (or an ephemeral CI runner) and confirm with
       PUI_SMOKE_ALLOW_HOST=1.
EOF
    exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
    if [ "${PUI_SMOKE_REEXEC:-}" = "1" ]; then
        fail "re-exec through sudo did not gain root"
    fi
    command -v sudo > /dev/null 2>&1 || fail "install.sh needs root and sudo is unavailable"
    echo "== re-executing as root (install.sh requires root) =="
    exec sudo env \
        PUI_SMOKE_REEXEC=1 \
        CI="${CI:-}" \
        PUI_SMOKE_ALLOW_HOST="${PUI_SMOKE_ALLOW_HOST:-}" \
        bash "$0" "$@"
fi

# Penhoon UI supports Ubuntu 22.04/24.04/26.04 and Debian 12+ only.
[ -r /etc/os-release ] || fail "/etc/os-release missing — unsupported OS"
# shellcheck source=/dev/null
. /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
    *" ubuntu "* | *" debian "*) : ;;
    *) fail "unsupported OS '${ID:-unknown}' — Ubuntu 22.04+/Debian 12+ only" ;;
esac
command -v apt-get > /dev/null 2>&1 || fail "apt-get is required"
command -v systemctl > /dev/null 2>&1 || fail "systemd is required"
[ -d /run/systemd/system ] || fail "systemd is not the running init — start the test on a real VM"

echo "== non-interactive install smoke test (${PRETTY_NAME:-${ID:-linux}}, version: ${PUI_SMOKE_VERSION:-latest}) =="

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl tar openssl ca-certificates > /dev/null

# --- install ----------------------------------------------------------------
export PUI_NONINTERACTIVE=1
export PUI_SSL_MODE=none
# PUI_DB_DSN is deliberately left unset: that exercises the cloud-init default
# where install.sh provisions a local PostgreSQL and writes the generated DSN.

echo "--- running install.sh piped (no TTY), version: ${PUI_SMOKE_VERSION:-latest} ---"
# Piping guarantees stdin is not a TTY, exercising the auto non-interactive path.
# shellcheck disable=SC2002 # the useless-looking cat is the point: it makes stdin a pipe
if [ -n "$PUI_SMOKE_VERSION" ]; then
    cat "$REPO_ROOT/install.sh" | bash -s -- "$PUI_SMOKE_VERSION"
else
    cat "$REPO_ROOT/install.sh" | bash
fi

# --- assertions -------------------------------------------------------------
echo "--- assertions ---"

test -f "$RESULT_FILE" || fail "$RESULT_FILE missing"
perms=$(stat -c %a "$RESULT_FILE")
[ "$perms" = "600" ] || fail "$RESULT_FILE perms=$perms (want 600)"

# install-result.env is written with printf '%q', so sourcing it is safe.
# shellcheck source=/dev/null
. "$RESULT_FILE"
[ -n "${PUI_USERNAME:-}" ] && [ "$PUI_USERNAME" != "admin" ] \
    || fail "username missing or still admin"
[ -n "${PUI_PASSWORD:-}" ] && [ "$PUI_PASSWORD" != "admin" ] \
    || fail "password missing or still admin"
[ -n "${PUI_PANEL_PORT:-}" ] || fail "port missing"
[ -n "${PUI_WEB_BASE_PATH:-}" ] || fail "web base path missing"

# PostgreSQL is the only backend: the panel refuses to start without PUI_DB_DSN.
test -f "$ENV_FILE" || fail "$ENV_FILE missing — PUI_DB_DSN must be pinned for the panel to start"
env_perms=$(stat -c %a "$ENV_FILE")
[ "$env_perms" = "600" ] || fail "$ENV_FILE perms=$env_perms (want 600)"
grep -Eq '^PUI_DB_DSN=["'"'"']?(postgres|postgresql)://.+' "$ENV_FILE" \
    || fail "$ENV_FILE has no postgres:// PUI_DB_DSN"

# PUI_DB_TYPE and every SQLite-only knob are gone — there is nothing to choose.
for dead in PUI_DB_TYPE PUI_DB_FOLDER PUI_DB_JOURNAL_MODE PUI_DB_CACHE_MB PUI_DB_MMAP_MB PUI_DB_SYNCHRONOUS; do
    if grep -q "^${dead}=" "$ENV_FILE" "$RESULT_FILE"; then
        fail "$dead is removed but the installer still writes it"
    fi
done

for stale in /etc/p-ui/p-ui.db /etc/p-ui/p-ui.db-wal /etc/p-ui/p-ui.db-shm /usr/local/p-ui/p-ui.db; do
    if [ -e "$stale" ]; then
        fail "SQLite artefact $stale exists — PostgreSQL is the only backend"
    fi
done

# The panel's backup/restore is pg_dump/pg_restore only.
command -v pg_dump > /dev/null 2>&1 \
    || fail "pg_dump missing — the panel's backup/restore needs postgresql-client"

# EnvironmentFile is systemd syntax, not shell: read the DSN literally instead of
# sourcing it, then export it for the p-ui CLI calls below (systemd hands the
# service its own copy).
PUI_DB_DSN=$(sed -n 's/^PUI_DB_DSN=//p' "$ENV_FILE" | head -n 1)
PUI_DB_DSN=${PUI_DB_DSN%[\"\']}
PUI_DB_DSN=${PUI_DB_DSN#[\"\']}
export PUI_DB_DSN
[ -n "$PUI_DB_DSN" ] || fail "PUI_DB_DSN is empty in $ENV_FILE"

if [ -n "$PUI_SMOKE_VERSION" ]; then
    installed=$(/usr/local/p-ui/p-ui -v)
    [ "$installed" = "${PUI_SMOKE_VERSION#v}" ] \
        || fail "installed version $installed, want ${PUI_SMOKE_VERSION#v}"
fi

# No default admin in the DB.
/usr/local/p-ui/p-ui setting -show | grep -q "hasDefaultCredential: false" \
    || fail "hasDefaultCredential is not false"

echo "--- verifying the panel serves HTTP ---"
systemctl cat p-ui.service > /dev/null 2>&1 || fail "p-ui.service was not installed"
systemctl restart p-ui
code=""
for _ in $(seq 1 30); do
    code=$(curl -s -o /dev/null -w "%{http_code}" \
        "http://127.0.0.1:${PUI_PANEL_PORT}/${PUI_WEB_BASE_PATH}/" 2> /dev/null || true)
    case "$code" in 200 | 301 | 302 | 307 | 308) break ;; esac
    sleep 1
done
echo "panel HTTP status: ${code:-none}"
case "${code:-}" in
    200 | 301 | 302 | 307 | 308) : ;;
    *)
        journalctl -u p-ui -n 40 --no-pager || true
        fail "panel did not serve (status ${code:-none})"
        ;;
esac
systemctl is-active --quiet p-ui || fail "the p-ui service is not active"

echo "SMOKE_PASS: user=$PUI_USERNAME port=$PUI_PANEL_PORT path=$PUI_WEB_BASE_PATH db=postgres"
echo "== non-interactive smoke test PASSED =="
