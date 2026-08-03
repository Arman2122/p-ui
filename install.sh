#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

pui_folder="${PUI_MAIN_FOLDER:=/usr/local/p-ui}"
pui_service="${PUI_SERVICE:=/etc/systemd/system}"
# Read by the systemd unit (EnvironmentFile=-/etc/default/p-ui); holds PUI_DB_DSN.
pui_env_file="/etc/default/p-ui"

# check root
[[ $EUID -ne 0 ]] && echo -e "${red}Fatal error: ${plain} Please run this script with root privilege \n " && exit 1

# Penhoon UI targets Ubuntu 22.04 / 24.04 / 26.04 and Debian 12+ exclusively.
# apt, systemd and iptables are assumed unconditionally from here on.
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "Failed to detect the operating system: no os-release file found." >&2
    exit 1
fi

unsupported_os() {
    echo -e "${red}Unsupported operating system: ${PRETTY_NAME:-${release} ${VERSION_ID:-unknown}}${plain}" >&2
    echo -e "${yellow}Penhoon UI supports Ubuntu 22.04 / 24.04 / 26.04 and Debian 12 or newer only.${plain}" >&2
    echo -e "${yellow}It requires apt, systemd and iptables; no other distribution or init system is supported.${plain}" >&2
    exit 1
}

case "${release}" in
    ubuntu)
        case "${VERSION_ID:-}" in
            22.04 | 24.04 | 26.04) ;;
            *) unsupported_os ;;
        esac
        ;;
    debian)
        debian_major="${VERSION_ID%%.*}"
        [[ "${debian_major}" =~ ^[0-9]+$ ]] && ((debian_major >= 12)) || unsupported_os
        ;;
    *)
        unsupported_os
        ;;
esac

if ! command -v apt-get > /dev/null 2>&1; then
    echo -e "${red}apt-get not found: Penhoon UI installs its dependencies with apt.${plain}" >&2
    exit 1
fi
if ! command -v systemctl > /dev/null 2>&1; then
    echo -e "${red}systemctl not found: Penhoon UI runs as a systemd service.${plain}" >&2
    exit 1
fi

# Every apt invocation below runs unattended.
export DEBIAN_FRONTEND=noninteractive

echo "The OS release is: ${PRETTY_NAME:-${release} ${VERSION_ID}}"

arch() {
    case "$(uname -m)" in
        x86_64 | x64 | amd64) echo 'amd64' ;;
        i*86 | x86) echo '386' ;;
        armv8* | armv8 | arm64 | aarch64) echo 'arm64' ;;
        armv7* | armv7 | arm) echo 'armv7' ;;
        armv6* | armv6) echo 'armv6' ;;
        armv5* | armv5) echo 'armv5' ;;
        s390x) echo 's390x' ;;
        *) echo -e "${green}Unsupported CPU architecture! ${plain}" && rm -f "$(realpath "$0")" && exit 1 ;;
    esac
}

echo "Arch: $(arch)"

# Non-interactive mode: triggered explicitly via PUI_NONINTERACTIVE=1, or
# implicitly when stdin is not a TTY (e.g. `curl ... | bash`, cloud-init).
# In this mode every prompt below is replaced by an env var or a sane default.
if [[ "${PUI_NONINTERACTIVE:-0}" == "1" ]] || [[ ! -t 0 ]]; then
    NONINTERACTIVE=1
else
    NONINTERACTIVE=0
fi
export NONINTERACTIVE

# Simple helpers
is_ipv4() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && return 0 || return 1
}
is_ipv6() {
    [[ "$1" =~ : ]] && return 0 || return 1
}
is_ip() {
    is_ipv4 "$1" || is_ipv6 "$1"
}
is_domain() {
    [[ "$1" =~ ^([A-Za-z0-9](-*[A-Za-z0-9])*\.)+(xn--[a-z0-9]{2,}|[A-Za-z]{2,})$ ]] && return 0 || return 1
}

# acme.sh's standalone server binds IPv4 by default; --listen-v6 makes it
# v6-only, which breaks HTTP-01 validation when the domain's A record points
# at this host's IPv4 (#4994). Only force IPv6 when the host has no global
# IPv4 address at all.
acme_listen_flag() {
    if ip -4 addr show scope global 2> /dev/null | grep -q "inet "; then
        echo ""
    else
        echo "--listen-v6"
    fi
}

# Port helpers
is_port_in_use() {
    local port="$1"
    if command -v ss > /dev/null 2>&1; then
        ss -ltn 2> /dev/null | awk -v p=":${port}$" '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v netstat > /dev/null 2>&1; then
        netstat -lnt 2> /dev/null | awk -v p=":${port} " '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v lsof > /dev/null 2>&1; then
        lsof -nP -iTCP:${port} -sTCP:LISTEN > /dev/null 2>&1 && return 0
    fi
    return 1
}

# sudo is a hard dependency, not a convenience: provisioning PostgreSQL runs
# every psql call as `sudo -u postgres`, and a Debian installed with a root
# password does not ship sudo. Without it the now-mandatory database setup
# would abort the whole install.
install_base() {
    apt-get update && apt-get install -y -q cron curl tar tzdata socat ca-certificates openssl iptables sudo
}

gen_random_string() {
    local length="$1"
    openssl rand -base64 $((length * 2)) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

# prompt_or_default VARNAME "prompt text" "default" [ENV_NAME]
# Interactive: read into VARNAME. Non-interactive: VARNAME = ${ENV_NAME:-default}.
# ENV_NAME defaults to VARNAME when omitted. Keeps every interactive prompt
# string byte-for-byte identical to the original `read -rp`.
prompt_or_default() {
    local __var="$1" __prompt="$2" __default="$3" __env="${4:-$1}"
    if [[ "$NONINTERACTIVE" == "1" ]]; then
        printf -v "$__var" '%s' "${!__env:-$__default}"
    else
        # shellcheck disable=SC2229
        read -rp "$__prompt" "$__var"
    fi
}

# write_install_result <user> <pass> <port> <webpath> <scheme> <host> <token>
# Persists a parseable, root-only credentials file consumed by cloud-init/MOTD.
# Values are written with printf '%q' so a pinned password/username containing
# spaces, quotes, $(...) or backticks is shell-escaped and the file stays safely
# source-able (consumers do '. install-result.env'). For the alphanumeric random
# values gen_random_string emits, %q is a no-op. This is a DIFFERENT file from the
# Postgres env file (/etc/default/p-ui), which holds the DSN and stays out of here.
write_install_result() {
    local u="$1" p="$2" port="$3" wbp="$4" scheme="$5" host="$6" token="$7"
    local result_file="/etc/p-ui/install-result.env"
    local url_host="${host:-SERVER_IP_UNKNOWN}"
    install -d -m 755 /etc/p-ui 2> /dev/null
    local prev_umask
    prev_umask=$(umask)
    umask 077
    if ! {
        printf 'PUI_USERNAME=%q\n' "$u"
        printf 'PUI_PASSWORD=%q\n' "$p"
        printf 'PUI_PANEL_PORT=%q\n' "$port"
        printf 'PUI_WEB_BASE_PATH=%q\n' "$wbp"
        printf 'PUI_ACCESS_URL=%q\n' "${scheme}://${url_host}:${port}/${wbp}"
        printf 'PUI_API_TOKEN=%q\n' "$token"
    } > "$result_file"; then
        umask "$prev_umask"
        echo -e "${yellow}Warning: failed to write ${result_file}.${plain}" >&2
        return 1
    fi
    umask "$prev_umask"
    chmod 600 "$result_file" 2> /dev/null
    chown root:root "$result_file" 2> /dev/null || true
    echo -e "${green}Install result written to ${result_file} (mode 600).${plain}"
}

# Debian/Ubuntu ship pg_hba.conf with scram-sha-256 for TCP logins, but a cluster
# that already existed on this host may have been switched to ident auth, which
# compares the OS username against the Postgres role and always rejects the
# randomly generated panel role over TCP (#5806). Prepend password-auth rules
# scoped to the panel database; first match wins, and md5 also accepts
# scram-sha-256-stored verifiers, so this is safe on a default cluster too.
pg_ensure_hba_password_auth() {
    local pg_db="$1"
    local hba_file
    hba_file=$(sudo -u postgres psql -tAc 'SHOW hba_file' 2> /dev/null | tr -d '[:space:]')
    [[ -n "${hba_file}" && -f "${hba_file}" ]] || return 0
    grep -Eq "^host[[:space:]]+${pg_db}[[:space:]]" "${hba_file}" && return 0
    local tmp
    tmp=$(mktemp) || return 1
    {
        echo "# Added by p-ui: allow password logins for the panel database."
        echo "host    ${pg_db}    all    127.0.0.1/32    md5"
        echo "host    ${pg_db}    all    ::1/128         md5"
        cat "${hba_file}"
    } > "${tmp}" || {
        rm -f "${tmp}"
        return 1
    }
    cat "${tmp}" > "${hba_file}" || {
        rm -f "${tmp}"
        return 1
    }
    rm -f "${tmp}"
    sudo -u postgres psql -tAc 'SELECT pg_reload_conf()' > /dev/null 2>&1 || true
}

install_postgres_local() {
    local pg_user pg_pass
    pg_pass=$(gen_random_string 24)
    local pg_db="pui"
    local pg_host="127.0.0.1"
    local pg_port="5432"

    # The Debian/Ubuntu postgresql package creates and starts the default
    # cluster for us, so there is no initdb step here.
    apt-get update >&2 && apt-get install -y -q postgresql >&2 || return 1
    systemctl enable --now postgresql >&2 || return 1

    # Wait briefly for the server to accept connections.
    local i
    for i in 1 2 3 4 5; do
        sudo -u postgres psql -tAc 'SELECT 1' > /dev/null 2>&1 && break
        sleep 1
    done

    local existing_owner=""
    existing_owner=$(sudo -u postgres psql -tAc \
        "SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname='${pg_db}'" 2> /dev/null \
        | tr -d '[:space:]')
    if [[ -n "${existing_owner}" && "${existing_owner}" != "postgres" ]]; then
        pg_user="${existing_owner}"
    else
        pg_user=$(gen_random_string 8)
    fi

    # Idempotent role/db creation. Identifiers are double-quoted because a
    # random username may start with a digit, which Postgres rejects unquoted.
    sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='${pg_user}'" 2> /dev/null \
        | grep -q 1 \
        || sudo -u postgres psql -c "CREATE USER \"${pg_user}\" WITH PASSWORD '${pg_pass}';" >&2 || return 1

    sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='${pg_db}'" 2> /dev/null \
        | grep -q 1 \
        || sudo -u postgres psql -c "CREATE DATABASE \"${pg_db}\" OWNER \"${pg_user}\";" >&2 || return 1

    sudo -u postgres psql -c "ALTER USER \"${pg_user}\" WITH PASSWORD '${pg_pass}';" >&2 || return 1

    pg_ensure_hba_password_auth "${pg_db}" \
        || echo -e "${yellow}Warning: could not update pg_hba.conf; PostgreSQL may reject the panel's TCP login (ident auth).${plain}" >&2

    local pg_pass_enc
    pg_pass_enc=$(printf '%s' "${pg_pass}" | sed -e 's/%/%25/g' -e 's/:/%3A/g' -e 's/@/%40/g' -e 's|/|%2F|g' -e 's/?/%3F/g' -e 's/#/%23/g')

    if [[ -n "${PG_CRED_FILE:-}" ]]; then
        local prev_umask
        prev_umask=$(umask)
        umask 077
        if ! cat > "${PG_CRED_FILE}" << EOF; then
PG_USER=${pg_user}
PG_PASS=${pg_pass}
PG_HOST=${pg_host}
PG_PORT=${pg_port}
PG_DB=${pg_db}
EOF
            umask "${prev_umask}"
            echo -e "${red}Failed to write PostgreSQL credentials to ${PG_CRED_FILE}${plain}" >&2
            return 1
        fi
        umask "${prev_umask}"
    fi

    echo "postgres://${pg_user}:${pg_pass_enc}@${pg_host}:${pg_port}/${pg_db}?sslmode=disable"
    return 0
}

ensure_pg_client() {
    if command -v pg_dump > /dev/null 2>&1 && command -v pg_restore > /dev/null 2>&1; then
        return 0
    fi
    echo -e "${yellow}Installing PostgreSQL client tools (pg_dump/pg_restore) for in-panel backup...${plain}" >&2
    apt-get update >&2 && apt-get install -y -q postgresql-client >&2 || return 1
    command -v pg_dump > /dev/null 2>&1 && command -v pg_restore > /dev/null 2>&1
}

# Wraps install_postgres_local: captures the generated DSN into PROVISIONED_DSN
# and the individual credential fields into PG_USER/PG_PASS/PG_HOST/PG_PORT/PG_DB
# so the post-install summary can print them. Returns non-zero on failure.
pg_provision_local() {
    PROVISIONED_DSN=""
    local cred_file
    cred_file=$(mktemp 2> /dev/null) || cred_file=$(mktemp -t p-ui-pg-creds.XXXXXXXX)
    if [[ -z "${cred_file}" ]]; then
        echo -e "${red}Failed to create a temporary credentials file.${plain}" >&2
        return 1
    fi
    if ! PROVISIONED_DSN=$(PG_CRED_FILE="${cred_file}" install_postgres_local); then
        rm -f "${cred_file}"
        return 1
    fi
    if [[ -r "${cred_file}" ]]; then
        # shellcheck disable=SC1090
        source "${cred_file}"
    fi
    rm -f "${cred_file}"
    PG_LOCAL_INSTALLED=1
    DB_LABEL="PostgreSQL (${PG_USER}@${PG_HOST}:${PG_PORT}/${PG_DB})"
    return 0
}

# A DSN is only a string until something actually connects with it, and the
# installer cannot notice a bad one on its own: `p-ui setting -show true` -- the
# first thing config_after_install runs -- prints "Database initialization
# failed: ..." but still exits 0 (main.go's setting case returns instead of
# exiting), so every grep in config_after_install just comes up empty and the
# script goes on to announce a successful install for a panel that will
# crash-loop. `p-ui migrate` runs the exact same database.InitDB the panel does
# and log.Fatal()s on failure, so it fails fast here with the panel's own
# actionable message and doubles as the first schema migration. It retries the
# connection with backoff for roughly a minute before giving up.
verify_database_dsn() {
    echo -e "${yellow}Verifying the PostgreSQL connection...${plain}"
    if "${pui_folder}/p-ui" migrate; then
        echo -e "${green}PostgreSQL connection verified.${plain}"
        return 0
    fi
    echo ""
    echo -e "${red}Cannot reach PostgreSQL with the configured PUI_DB_DSN -- aborting the install.${plain}"
    echo -e "${yellow}Penhoon UI stores all of its data in PostgreSQL and has no other backend.${plain}"
    echo -e "${yellow}Fix PUI_DB_DSN in ${pui_env_file} and run the installer again, for example:${plain}"
    echo -e "  ${blue}PUI_DB_DSN=postgres://p-ui:PASSWORD@127.0.0.1:5432/p-ui?sslmode=disable${plain}"
    echo -e "${yellow}Only this postgres:// URL form is accepted -- libpq key=value strings are rejected.${plain}"
    echo -e "${yellow}Percent-encode any of : / ? # @ % that appear in the password.${plain}"
    exit 1
}

# PostgreSQL is the only supported backend, so a DSN must exist before the panel
# binary is ever invoked -- every `p-ui setting` call in config_after_install
# already needs a live database. Sets PUI_DB_DSN (exported), DB_LABEL and
# PG_LOCAL_INSTALLED, and persists the DSN into ${pui_env_file}, which the
# systemd unit reads via EnvironmentFile.
setup_database() {
    PG_LOCAL_INSTALLED=0
    DB_LABEL="PostgreSQL (external)"
    local pui_dsn=""
    local pg_mode=""
    local pg_fail=""

    # Re-running the installer (`p-ui install`, or the curl one-liner) must not
    # re-provision a database that is already wired up, and must never clobber
    # an external DSN.
    if [[ -z "${PUI_DB_DSN:-}" && -r "${pui_env_file}" ]]; then
        local existing_dsn
        existing_dsn=$(grep -m1 '^PUI_DB_DSN=' "${pui_env_file}" 2> /dev/null | cut -d= -f2-)
        # EnvironmentFile syntax permits a quoted value; systemd and the CLI's
        # dotenv loader strip those quotes, so strip them here too -- otherwise
        # the DSN exported below would carry a literal leading quote.
        existing_dsn=${existing_dsn%[\"\']}
        existing_dsn=${existing_dsn#[\"\']}
        if [[ -n "${existing_dsn}" ]]; then
            echo -e "${green}Reusing the PostgreSQL DSN already configured in ${pui_env_file}.${plain}"
            PUI_DB_DSN="${existing_dsn}"
            DB_LABEL="PostgreSQL (configured in ${pui_env_file})"
        fi
    fi

    if [[ -n "${PUI_DB_DSN:-}" ]]; then
        pui_dsn="${PUI_DB_DSN}"
    elif [[ "$NONINTERACTIVE" == "1" ]]; then
        echo -e "${yellow}Installing PostgreSQL locally (non-interactive)...${plain}"
        if pg_provision_local; then
            pui_dsn="${PROVISIONED_DSN}"
        else
            echo -e "${red}PostgreSQL installation failed; aborting.${plain}"
            echo -e "${yellow}Set PUI_DB_DSN=postgres://user:pass@host:5432/dbname?sslmode=disable to use an existing server.${plain}"
            exit 1
        fi
    else
        while [[ -z "$pui_dsn" ]]; do
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}     PostgreSQL Database                   ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "  1) Install PostgreSQL locally and create a dedicated user/db (recommended)"
            echo -e "  2) Use an existing PostgreSQL server (enter DSN)"
            read -rp "Choose [1]: " pg_mode
            pg_mode="${pg_mode:-1}"
            if [[ "$pg_mode" == "2" ]]; then
                while [[ -z "$pui_dsn" ]]; do
                    read -rp "Enter PostgreSQL DSN (postgres://user:pass@host:port/dbname?sslmode=disable): " pui_dsn
                    # Trim surrounding whitespace only. requireDSN() in
                    # internal/database/db.go takes the postgres:// URL form and
                    # nothing else -- libpq's "host=... dbname=..." key=value
                    # string is rejected -- and a URL never carries a literal
                    # space, so leading/trailing padding is all there is to strip.
                    pui_dsn="${pui_dsn#"${pui_dsn%%[![:space:]]*}"}"
                    pui_dsn="${pui_dsn%"${pui_dsn##*[![:space:]]}"}"
                done
                DB_LABEL="PostgreSQL (external)"
            else
                echo -e "${yellow}Installing PostgreSQL — this may take a moment...${plain}"
                if pg_provision_local; then
                    pui_dsn="${PROVISIONED_DSN}"
                else
                    echo ""
                    echo -e "${red}PostgreSQL installation failed.${plain}"
                    echo -e "  1) Back to the database menu (retry, or enter an external DSN)"
                    echo -e "  2) Abort install"
                    read -rp "Choose [1]: " pg_fail
                    if [[ "${pg_fail:-1}" == "2" ]]; then
                        echo -e "${red}Install aborted.${plain}"
                        exit 1
                    fi
                fi
            fi
        done
    fi

    # This is the service-wide EnvironmentFile: systemd loads every PUI_* knob
    # from it (PUI_DB_MAX_OPEN_CONNS, PUI_LOG_LEVEL, PUI_TUNNEL_HEALTH_*, ...)
    # and so does the CLI. Rewrite only the PUI_DB_DSN line so re-running the
    # installer never wipes an operator's other settings -- same contract as
    # pg_write_env() in p-ui.sh.
    install -d -m 755 "$(dirname "${pui_env_file}")"
    local prev_umask
    prev_umask=$(umask)
    umask 077
    if ! touch "${pui_env_file}"; then
        umask "${prev_umask}"
        echo -e "${red}Failed to create ${pui_env_file}; the panel cannot start without PUI_DB_DSN.${plain}"
        exit 1
    fi
    sed -i '/^PUI_DB_DSN=/d' "${pui_env_file}"
    if ! printf 'PUI_DB_DSN=%s\n' "${pui_dsn}" >> "${pui_env_file}"; then
        umask "${prev_umask}"
        echo -e "${red}Failed to write ${pui_env_file}; the panel cannot start without PUI_DB_DSN.${plain}"
        exit 1
    fi
    umask "${prev_umask}"
    chmod 600 "${pui_env_file}"
    chown root:root "${pui_env_file}" 2> /dev/null || true
    export PUI_DB_DSN="${pui_dsn}"

    ensure_pg_client || echo -e "${yellow}⚠ Could not install pg_dump/pg_restore. In-panel database backup/restore will be unavailable until you install the postgresql-client package.${plain}"

    verify_database_dsn
}

install_acme() {
    echo -e "${green}Installing acme.sh for SSL certificate management...${plain}"
    # The `cd ~` stays inside a subshell on purpose. install_p-ui runs from the
    # freshly extracted ${pui_folder} and this function is reached from
    # config_after_install -> prompt_and_setup_ssl, so leaking the cd would move
    # the caller's working directory to /root for the rest of the install.
    if ! (cd ~ && curl -s https://get.acme.sh | sh) > /dev/null 2>&1; then
        echo -e "${red}Failed to install acme.sh${plain}"
        return 1
    fi
    echo -e "${green}acme.sh installed successfully${plain}"
    return 0
}

setup_ssl_certificate() {
    local domain="$1"
    local server_ip="$2"
    local existing_port="$3"
    local existing_webBasePath="$4"

    echo -e "${green}Setting up SSL certificate...${plain}"

    # Check if acme.sh is installed
    if ! command -v ~/.acme.sh/acme.sh &> /dev/null; then
        install_acme
        if [ $? -ne 0 ]; then
            echo -e "${yellow}Failed to install acme.sh, skipping SSL setup${plain}"
            return 1
        fi
    fi

    # Create certificate directory
    local certPath="/root/cert/${domain}"
    mkdir -p "$certPath"

    # Issue certificate
    echo -e "${green}Issuing SSL certificate for ${domain}...${plain}"
    echo -e "${yellow}Note: Port 80 must be open and accessible from the internet${plain}"

    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force > /dev/null 2>&1
    ~/.acme.sh/acme.sh --issue -d ${domain} $(acme_listen_flag) --standalone --httpport 80 --force

    if [ $? -ne 0 ]; then
        echo -e "${yellow}Failed to issue certificate for ${domain}${plain}"
        echo -e "${yellow}Please ensure port 80 is open and try again later with: p-ui${plain}"
        rm -rf ~/.acme.sh/${domain} ~/.acme.sh/${domain}_ecc 2> /dev/null
        rm -rf "$certPath" 2> /dev/null
        return 1
    fi

    # Install certificate
    ~/.acme.sh/acme.sh --installcert --force -d ${domain} \
        --key-file /root/cert/${domain}/privkey.pem \
        --fullchain-file /root/cert/${domain}/fullchain.pem \
        --reloadcmd "systemctl restart p-ui" > /dev/null 2>&1

    if [ $? -ne 0 ]; then
        echo -e "${yellow}Failed to install certificate${plain}"
        return 1
    fi

    # Enable auto-renew
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade > /dev/null 2>&1
    # Secure permissions: private key readable only by owner
    chmod 600 $certPath/privkey.pem 2> /dev/null
    chmod 644 $certPath/fullchain.pem 2> /dev/null

    # Set certificate for panel
    local webCertFile="/root/cert/${domain}/fullchain.pem"
    local webKeyFile="/root/cert/${domain}/privkey.pem"

    if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
        ${pui_folder}/p-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile" > /dev/null 2>&1
        echo -e "${green}SSL certificate installed and configured successfully!${plain}"
        return 0
    else
        echo -e "${yellow}Certificate files not found${plain}"
        return 1
    fi
}

# Issue Let's Encrypt IP certificate with shortlived profile (~6 days validity)
# Requires acme.sh and port 80 open for HTTP-01 challenge
setup_ip_certificate() {
    local ipv4="$1"
    local ipv6="$2" # optional

    echo -e "${green}Setting up Let's Encrypt IP certificate (shortlived profile)...${plain}"
    echo -e "${yellow}Note: IP certificates are valid for ~6 days and will auto-renew.${plain}"
    echo -e "${yellow}Default listener is port 80. If you choose another port, ensure external port 80 forwards to it.${plain}"

    # Check for acme.sh
    if ! command -v ~/.acme.sh/acme.sh &> /dev/null; then
        install_acme
        if [ $? -ne 0 ]; then
            echo -e "${red}Failed to install acme.sh${plain}"
            return 1
        fi
    fi

    # Validate IP address
    if [[ -z "$ipv4" ]]; then
        echo -e "${red}IPv4 address is required${plain}"
        return 1
    fi

    if ! is_ipv4 "$ipv4"; then
        echo -e "${red}Invalid IPv4 address: $ipv4${plain}"
        return 1
    fi

    # Create certificate directory
    local certDir="/root/cert/ip"
    mkdir -p "$certDir"

    # Build domain arguments
    local domain_args="-d ${ipv4}"
    if [[ -n "$ipv6" ]] && is_ipv6 "$ipv6"; then
        domain_args="${domain_args} -d ${ipv6}"
        echo -e "${green}Including IPv6 address: ${ipv6}${plain}"
    fi

    # Set reload command for auto-renewal (add || true so it doesn't fail during first install)
    local reloadCmd="systemctl restart p-ui 2>/dev/null || true"

    # Choose port for HTTP-01 listener (default 80, prompt override)
    local WebPort=""
    prompt_or_default WebPort "Port to use for ACME HTTP-01 listener (default 80): " "80" PUI_ACME_HTTP_PORT
    WebPort="${WebPort:-80}"
    if ! [[ "${WebPort}" =~ ^[0-9]+$ ]] || ((WebPort < 1 || WebPort > 65535)); then
        echo -e "${red}Invalid port provided. Falling back to 80.${plain}"
        WebPort=80
    fi
    echo -e "${green}Using port ${WebPort} for standalone validation.${plain}"
    if [[ "${WebPort}" -ne 80 ]]; then
        echo -e "${yellow}Reminder: Let's Encrypt still connects on port 80; forward external port 80 to ${WebPort}.${plain}"
    fi

    # Ensure chosen port is available
    while true; do
        if is_port_in_use "${WebPort}"; then
            echo -e "${yellow}Port ${WebPort} is in use.${plain}"

            local alt_port=""
            if [[ "$NONINTERACTIVE" == "1" ]]; then
                echo -e "${red}Port ${WebPort} is busy; cannot proceed in non-interactive mode.${plain}"
                return 1
            fi
            read -rp "Enter another port for acme.sh standalone listener (leave empty to abort): " alt_port
            alt_port="${alt_port// /}"
            if [[ -z "${alt_port}" ]]; then
                echo -e "${red}Port ${WebPort} is busy; cannot proceed.${plain}"
                return 1
            fi
            if ! [[ "${alt_port}" =~ ^[0-9]+$ ]] || ((alt_port < 1 || alt_port > 65535)); then
                echo -e "${red}Invalid port provided.${plain}"
                return 1
            fi
            WebPort="${alt_port}"
            continue
        else
            echo -e "${green}Port ${WebPort} is free and ready for standalone validation.${plain}"
            break
        fi
    done

    # Issue certificate with shortlived profile
    echo -e "${green}Issuing IP certificate for ${ipv4}...${plain}"
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force > /dev/null 2>&1
    [[ -n "${PUI_ACME_EMAIL:-}" ]] && ~/.acme.sh/acme.sh --register-account -m "${PUI_ACME_EMAIL}" > /dev/null 2>&1

    ~/.acme.sh/acme.sh --issue \
        ${domain_args} \
        --standalone \
        --server letsencrypt \
        --certificate-profile shortlived \
        --days 6 \
        --httpport ${WebPort} \
        --force

    if [ $? -ne 0 ]; then
        echo -e "${red}Failed to issue IP certificate${plain}"
        echo -e "${yellow}Please ensure port ${WebPort} is reachable (or forwarded from external port 80)${plain}"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf ~/.acme.sh/${ipv4} ~/.acme.sh/${ipv4}_ecc 2> /dev/null
        [[ -n "$ipv6" ]] && rm -rf ~/.acme.sh/${ipv6} ~/.acme.sh/${ipv6}_ecc 2> /dev/null
        rm -rf ${certDir} 2> /dev/null
        return 1
    fi

    echo -e "${green}Certificate issued successfully, installing...${plain}"

    # Install certificate
    # Note: acme.sh may report "Reload error" and exit non-zero if reloadcmd fails,
    # but the cert files are still installed. We check for files instead of exit code.
    ~/.acme.sh/acme.sh --installcert --force -d ${ipv4} \
        --key-file "${certDir}/privkey.pem" \
        --fullchain-file "${certDir}/fullchain.pem" \
        --reloadcmd "${reloadCmd}" 2>&1 || true

    # Verify certificate files exist (don't rely on exit code - reloadcmd failure causes non-zero)
    if [[ ! -f "${certDir}/fullchain.pem" || ! -f "${certDir}/privkey.pem" ]]; then
        echo -e "${red}Certificate files not found after installation${plain}"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf ~/.acme.sh/${ipv4} ~/.acme.sh/${ipv4}_ecc 2> /dev/null
        [[ -n "$ipv6" ]] && rm -rf ~/.acme.sh/${ipv6} ~/.acme.sh/${ipv6}_ecc 2> /dev/null
        rm -rf ${certDir} 2> /dev/null
        return 1
    fi

    echo -e "${green}Certificate files installed successfully${plain}"

    # Enable auto-upgrade for acme.sh (ensures cron job runs)
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade > /dev/null 2>&1

    # Secure permissions: private key readable only by owner
    chmod 600 ${certDir}/privkey.pem 2> /dev/null
    chmod 644 ${certDir}/fullchain.pem 2> /dev/null

    # Configure panel to use the certificate
    echo -e "${green}Setting certificate paths for the panel...${plain}"
    ${pui_folder}/p-ui cert -webCert "${certDir}/fullchain.pem" -webCertKey "${certDir}/privkey.pem"

    if [ $? -ne 0 ]; then
        echo -e "${yellow}Warning: Could not set certificate paths automatically${plain}"
        echo -e "${yellow}Certificate files are at:${plain}"
        echo -e "  Cert: ${certDir}/fullchain.pem"
        echo -e "  Key:  ${certDir}/privkey.pem"
    else
        echo -e "${green}Certificate paths configured successfully${plain}"
    fi

    echo -e "${green}IP certificate installed and configured successfully!${plain}"
    echo -e "${green}Certificate valid for ~6 days, auto-renews via acme.sh cron job.${plain}"
    echo -e "${yellow}acme.sh will automatically renew and reload p-ui before expiry.${plain}"
    return 0
}

# Comprehensive manual SSL certificate issuance via acme.sh
ssl_cert_issue() {
    local existing_webBasePath=$(${pui_folder}/p-ui setting -show true | grep 'webBasePath:' | awk -F': ' '{print $2}' | tr -d '[:space:]' | sed 's#^/##')
    local existing_port=$(${pui_folder}/p-ui setting -show true | grep 'port:' | awk -F': ' '{print $2}' | tr -d '[:space:]')

    # check for acme.sh first
    if ! command -v ~/.acme.sh/acme.sh &> /dev/null; then
        echo "acme.sh could not be found. Installing now..."
        # Subshell: see install_acme -- the caller's working directory must
        # survive this, install_p-ui installs the systemd unit from it later.
        if ! (cd ~ && curl -s https://get.acme.sh | sh); then
            echo -e "${red}Failed to install acme.sh${plain}"
            return 1
        fi
        echo -e "${green}acme.sh installed successfully${plain}"
    fi

    # get the domain here, and we need to verify it
    local domain=""
    if [[ "$NONINTERACTIVE" == "1" ]]; then
        domain="${PUI_DOMAIN// /}"
        if [[ -z "$domain" ]] || ! is_domain "$domain"; then
            echo -e "${red}PUI_SSL_MODE=domain requires a valid PUI_DOMAIN (got: '${PUI_DOMAIN:-}').${plain}"
            return 1
        fi
    else
        while true; do
            read -rp "Please enter your domain name: " domain
            domain="${domain// /}" # Trim whitespace

            if [[ -z "$domain" ]]; then
                echo -e "${red}Domain name cannot be empty. Please try again.${plain}"
                continue
            fi

            if ! is_domain "$domain"; then
                echo -e "${red}Invalid domain format: ${domain}. Please enter a valid domain name.${plain}"
                continue
            fi

            break
        done
    fi
    echo -e "${green}Your domain is: ${domain}, checking it...${plain}"
    SSL_ISSUED_DOMAIN="${domain}"

    # detect existing certificate and reuse it only if its files are actually
    # present and non-empty. acme.sh stores ECC certs under ${domain}_ecc and RSA
    # certs under ${domain}; a failed issuance can leave a domain entry in --list
    # with no usable cert files, which must not be reused (it produces a 0-byte
    # fullchain.pem). Broken partial state is cleaned up so issuance can proceed.
    local cert_exists=0
    if ~/.acme.sh/acme.sh --list 2> /dev/null | awk '{print $1}' | grep -Fxq "${domain}"; then
        local acmeCertDir=""
        if [[ -s ~/.acme.sh/${domain}_ecc/fullchain.cer && -s ~/.acme.sh/${domain}_ecc/${domain}.key ]]; then
            acmeCertDir=~/.acme.sh/${domain}_ecc
        elif [[ -s ~/.acme.sh/${domain}/fullchain.cer && -s ~/.acme.sh/${domain}/${domain}.key ]]; then
            acmeCertDir=~/.acme.sh/${domain}
        fi
        if [[ -n "${acmeCertDir}" ]]; then
            cert_exists=1
            local certInfo=$(~/.acme.sh/acme.sh --list 2> /dev/null | grep -F "${domain}")
            echo -e "${yellow}Existing certificate found for ${domain}, will reuse it.${plain}"
            [[ -n "${certInfo}" ]] && echo "$certInfo"
        else
            echo -e "${yellow}Found incomplete acme.sh state for ${domain} (no valid certificate files); cleaning it up and re-issuing.${plain}"
            rm -rf ~/.acme.sh/${domain} ~/.acme.sh/${domain}_ecc
        fi
    fi
    if [[ ${cert_exists} -eq 0 ]]; then
        echo -e "${green}Your domain is ready for issuing certificates now...${plain}"
    fi

    # create a directory for the certificate
    certPath="/root/cert/${domain}"
    if [ ! -d "$certPath" ]; then
        mkdir -p "$certPath"
    else
        rm -rf "$certPath"
        mkdir -p "$certPath"
    fi

    # get the port number for the standalone server
    local WebPort=80
    prompt_or_default WebPort "Please choose which port to use (default is 80): " "80" PUI_ACME_HTTP_PORT
    if [[ -z ${WebPort} ]]; then
        WebPort=80
    elif [[ ! ${WebPort} =~ ^[1-9][0-9]*$ || ${WebPort} -gt 65535 ]]; then
        echo -e "${yellow}Your input ${WebPort} is invalid, will use default port 80.${plain}"
        WebPort=80
    fi
    echo -e "${green}Will use port: ${WebPort} to issue certificates. Please make sure this port is open.${plain}"

    # Stop panel temporarily
    echo -e "${yellow}Stopping panel temporarily...${plain}"
    systemctl stop p-ui 2> /dev/null

    if [[ ${cert_exists} -eq 0 ]]; then
        # issue the certificate
        ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force
        [[ -n "${PUI_ACME_EMAIL:-}" ]] && ~/.acme.sh/acme.sh --register-account -m "${PUI_ACME_EMAIL}" > /dev/null 2>&1
        ~/.acme.sh/acme.sh --issue -d ${domain} $(acme_listen_flag) --standalone --httpport ${WebPort} --force
        if [ $? -ne 0 ]; then
            echo -e "${red}Issuing certificate failed, please check logs.${plain}"
            rm -rf ~/.acme.sh/${domain} ~/.acme.sh/${domain}_ecc
            systemctl start p-ui 2> /dev/null
            return 1
        else
            echo -e "${green}Issuing certificate succeeded, installing certificates...${plain}"
        fi
    else
        echo -e "${green}Using existing certificate, installing certificates...${plain}"
    fi

    # Setup reload command
    reloadCmd="systemctl restart p-ui"
    echo -e "${green}Default --reloadcmd for ACME is: ${yellow}systemctl restart p-ui${plain}"
    echo -e "${green}This command will run on every certificate issue and renew.${plain}"
    if [[ "$NONINTERACTIVE" == "1" ]]; then
        setReloadcmd="n"
    else
        read -rp "Would you like to modify --reloadcmd for ACME? (y/n): " setReloadcmd
    fi
    if [[ "$setReloadcmd" == "y" || "$setReloadcmd" == "Y" ]]; then
        echo -e "\n${green}\t1.${plain} Preset: systemctl reload nginx ; systemctl restart p-ui"
        echo -e "${green}\t2.${plain} Input your own command"
        echo -e "${green}\t0.${plain} Keep default reloadcmd"
        read -rp "Choose an option: " choice
        case "$choice" in
            1)
                echo -e "${green}Reloadcmd is: systemctl reload nginx ; systemctl restart p-ui${plain}"
                reloadCmd="systemctl reload nginx ; systemctl restart p-ui"
                ;;
            2)
                echo -e "${yellow}It's recommended to put p-ui restart at the end${plain}"
                read -rp "Please enter your custom reloadcmd: " reloadCmd
                echo -e "${green}Reloadcmd is: ${reloadCmd}${plain}"
                ;;
            *)
                echo -e "${green}Keeping default reloadcmd${plain}"
                ;;
        esac
    fi

    # install the certificate
    local installOutput=""
    installOutput=$(~/.acme.sh/acme.sh --installcert --force -d ${domain} \
        --key-file /root/cert/${domain}/privkey.pem \
        --fullchain-file /root/cert/${domain}/fullchain.pem --reloadcmd "${reloadCmd}" 2>&1)
    local installRc=$?
    echo "${installOutput}"

    local installWroteFiles=0
    if echo "${installOutput}" | grep -q "Installing key to:" && echo "${installOutput}" | grep -q "Installing full chain to:"; then
        installWroteFiles=1
    fi

    if [[ -f "/root/cert/${domain}/privkey.pem" && -f "/root/cert/${domain}/fullchain.pem" && (${installRc} -eq 0 || ${installWroteFiles} -eq 1) ]]; then
        echo -e "${green}Installing certificate succeeded, enabling auto renew...${plain}"
    else
        echo -e "${red}Installing certificate failed, exiting.${plain}"
        if [[ ${cert_exists} -eq 0 ]]; then
            rm -rf ~/.acme.sh/${domain} ~/.acme.sh/${domain}_ecc
        fi
        systemctl start p-ui 2> /dev/null
        return 1
    fi

    # enable auto-renew
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade
    if [ $? -ne 0 ]; then
        echo -e "${yellow}Auto renew setup had issues, certificate details:${plain}"
        ls -lah /root/cert/${domain}/
        # Secure permissions: private key readable only by owner
        chmod 600 $certPath/privkey.pem 2> /dev/null
        chmod 644 $certPath/fullchain.pem 2> /dev/null
    else
        echo -e "${green}Auto renew succeeded, certificate details:${plain}"
        ls -lah /root/cert/${domain}/
        # Secure permissions: private key readable only by owner
        chmod 600 $certPath/privkey.pem 2> /dev/null
        chmod 644 $certPath/fullchain.pem 2> /dev/null
    fi

    # start panel
    systemctl start p-ui 2> /dev/null

    # Prompt user to set panel paths after successful certificate installation
    if [[ "$NONINTERACTIVE" == "1" ]]; then
        setPanel="y"
    else
        read -rp "Would you like to set this certificate for the panel? (y/n): " setPanel
    fi
    if [[ "$setPanel" == "y" || "$setPanel" == "Y" ]]; then
        local webCertFile="/root/cert/${domain}/fullchain.pem"
        local webKeyFile="/root/cert/${domain}/privkey.pem"

        if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
            ${pui_folder}/p-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
            echo -e "${green}Certificate paths set for the panel${plain}"
            echo -e "${green}Certificate File: $webCertFile${plain}"
            echo -e "${green}Private Key File: $webKeyFile${plain}"
            echo ""
            echo -e "${green}Access URL: https://${domain}:${existing_port}/${existing_webBasePath}${plain}"
            echo -e "${yellow}Panel will restart to apply SSL certificate...${plain}"
            systemctl restart p-ui 2> /dev/null
        else
            echo -e "${red}Error: Certificate or private key file not found for domain: $domain.${plain}"
        fi
    else
        echo -e "${yellow}Skipping panel path setting.${plain}"
    fi

    return 0
}

# Reusable interactive SSL setup (domain or IP)
# Sets global `SSL_HOST` to the chosen domain/IP for Access URL usage
prompt_and_setup_ssl() {
    local panel_port="$1"
    local web_base_path="$2"
    local server_ip="$3"

    local ssl_choice=""
    SSL_SCHEME="https"

    echo -e "${yellow}Choose SSL certificate setup method:${plain}"
    echo -e "${green}1.${plain} Let's Encrypt for Domain (90-day validity, auto-renews)"
    echo -e "${green}2.${plain} Let's Encrypt for IP Address (6-day validity, auto-renews)"
    echo -e "${green}3.${plain} Custom SSL Certificate (Path to existing files)"
    echo -e "${green}4.${plain} Skip SSL (advanced — behind reverse proxy / SSH tunnel only)"
    echo -e "${blue}Note:${plain} Options 1 & 2 require port 80 open. Option 3 requires manual paths."
    echo -e "${blue}Note:${plain} Option 4 serves the panel over plain HTTP — only safe behind nginx/Caddy or an SSH tunnel."
    if [[ "$NONINTERACTIVE" == "1" ]]; then
        case "${PUI_SSL_MODE:-none}" in
            domain) ssl_choice="1" ;;
            ip) ssl_choice="2" ;;
            none | "") ssl_choice="4" ;;
            *)
                echo -e "${yellow}Unknown PUI_SSL_MODE='${PUI_SSL_MODE}', defaulting to none (HTTP).${plain}"
                ssl_choice="4"
                ;;
        esac
    else
        read -rp "Choose an option (default 2 for IP): " ssl_choice
        ssl_choice="${ssl_choice// /}" # Trim whitespace

        # Default to 2 (IP cert) if input is empty or invalid (not 1, 3 or 4)
        if [[ "$ssl_choice" != "1" && "$ssl_choice" != "3" && "$ssl_choice" != "4" ]]; then
            ssl_choice="2"
        fi
    fi

    case "$ssl_choice" in
        1)
            # User chose Let's Encrypt domain option
            echo -e "${green}Using Let's Encrypt for domain certificate...${plain}"
            if ssl_cert_issue; then
                local cert_domain="${SSL_ISSUED_DOMAIN}"
                if [[ -z "${cert_domain}" ]]; then
                    cert_domain=$(~/.acme.sh/acme.sh --list 2> /dev/null | tail -1 | awk '{print $1}')
                fi

                if [[ -n "${cert_domain}" ]]; then
                    SSL_HOST="${cert_domain}"
                    echo -e "${green}✓ SSL certificate configured successfully with domain: ${cert_domain}${plain}"
                else
                    echo -e "${yellow}SSL setup may have completed, but domain extraction failed${plain}"
                    SSL_HOST="${server_ip}"
                fi
            else
                echo -e "${red}SSL certificate setup failed for domain mode.${plain}"
                SSL_HOST="${server_ip}"
            fi
            ;;
        2)
            # User chose Let's Encrypt IP certificate option
            echo -e "${green}Using Let's Encrypt for IP certificate (shortlived profile)...${plain}"

            # Confirm the auto-detected IP before issuing for it: with asymmetric
            # routing / multi-WAN the echo services can return a transit address.
            if [[ "$NONINTERACTIVE" != "1" ]]; then
                local ip_confirm=""
                read -rp "Is ${server_ip} the correct incoming public IPv4 address for this server? [Default y]: " ip_confirm
                if [[ -n "$ip_confirm" && "$ip_confirm" != "y" && "$ip_confirm" != "Y" ]]; then
                    server_ip=""
                    while [[ -z "$server_ip" ]]; do
                        read -rp "Please enter your server's public IPv4 address: " server_ip
                        server_ip="${server_ip// /}"
                        if [[ ! "$server_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                            echo -e "${red}Invalid IPv4 address. Please try again.${plain}"
                            server_ip=""
                        fi
                    done
                fi
            fi

            # Ask for optional IPv6
            local ipv6_addr=""
            prompt_or_default ipv6_addr "Do you have an IPv6 address to include? (leave empty to skip): " "" PUI_SSL_IPV6
            ipv6_addr="${ipv6_addr// /}" # Trim whitespace

            # Stop panel if running (port 80 needed)
            systemctl stop p-ui > /dev/null 2>&1

            setup_ip_certificate "${server_ip}" "${ipv6_addr}"
            if [ $? -eq 0 ]; then
                SSL_HOST="${server_ip}"
                echo -e "${green}✓ Let's Encrypt IP certificate configured successfully${plain}"
            else
                echo -e "${red}✗ IP certificate setup failed. Please check port 80 is open.${plain}"
                SSL_HOST="${server_ip}"
            fi
            ;;
        3)
            # User chose Custom Paths (User Provided) option
            echo -e "${green}Using custom existing certificate...${plain}"
            local custom_cert=""
            local custom_key=""
            local custom_domain=""

            # 3.1 Request Domain to compose Panel URL later
            read -rp "Please enter domain name certificate issued for: " custom_domain
            custom_domain="${custom_domain// /}" # Remove spaces

            # 3.2 Loop for Certificate Path
            while true; do
                read -rp "Input certificate path (keywords: .crt / fullchain): " custom_cert
                # Strip quotes if present
                custom_cert=$(echo "$custom_cert" | tr -d '"' | tr -d "'")

                if [[ -f "$custom_cert" && -r "$custom_cert" && -s "$custom_cert" ]]; then
                    break
                elif [[ ! -f "$custom_cert" ]]; then
                    echo -e "${red}Error: File does not exist! Try again.${plain}"
                elif [[ ! -r "$custom_cert" ]]; then
                    echo -e "${red}Error: File exists but is not readable (check permissions)!${plain}"
                else
                    echo -e "${red}Error: File is empty!${plain}"
                fi
            done

            # 3.3 Loop for Private Key Path
            while true; do
                read -rp "Input private key path (keywords: .key / privatekey): " custom_key
                # Strip quotes if present
                custom_key=$(echo "$custom_key" | tr -d '"' | tr -d "'")

                if [[ -f "$custom_key" && -r "$custom_key" && -s "$custom_key" ]]; then
                    break
                elif [[ ! -f "$custom_key" ]]; then
                    echo -e "${red}Error: File does not exist! Try again.${plain}"
                elif [[ ! -r "$custom_key" ]]; then
                    echo -e "${red}Error: File exists but is not readable (check permissions)!${plain}"
                else
                    echo -e "${red}Error: File is empty!${plain}"
                fi
            done

            # 3.4 Apply Settings via p-ui binary
            ${pui_folder}/p-ui cert -webCert "$custom_cert" -webCertKey "$custom_key" > /dev/null 2>&1

            # Set SSL_HOST for composing Panel URL
            if [[ -n "$custom_domain" ]]; then
                SSL_HOST="$custom_domain"
            else
                SSL_HOST="${server_ip}"
            fi

            echo -e "${green}✓ Custom certificate paths applied.${plain}"
            echo -e "${yellow}Note: You are responsible for renewing these files externally.${plain}"

            systemctl restart p-ui > /dev/null 2>&1
            ;;
        4)
            echo ""
            echo -e "${red}⚠ Panel will be installed WITHOUT SSL/TLS.${plain}"
            echo -e "${yellow}Login credentials and cookies will travel as plain HTTP.${plain}"
            echo -e "${yellow}Only safe when:${plain}"
            echo -e "${yellow}  • A reverse proxy (nginx, Caddy, Traefik) terminates TLS for you, or${plain}"
            echo -e "${yellow}  • You access the panel exclusively via SSH tunnel${plain}"
            echo ""

            SSL_SCHEME="http"
            SSL_HOST="${server_ip}"

            local bind_local=""
            if [[ "$NONINTERACTIVE" == "1" ]]; then
                # Cloud images must stay reachable on their public interface.
                bind_local="n"
            else
                read -rp "Bind the panel to 127.0.0.1 only? (recommended — forces SSH tunnel / reverse-proxy access) [y/N]: " bind_local
            fi
            if [[ "$bind_local" == "y" || "$bind_local" == "Y" ]]; then
                ${pui_folder}/p-ui setting -listenIP "127.0.0.1" > /dev/null 2>&1
                SSL_HOST="127.0.0.1"
                echo -e "${green}✓ Panel bound to 127.0.0.1 only. It is now unreachable from the public internet.${plain}"
                echo ""
                echo -e "${green}SSH Port Forwarding — open the panel from your local machine via:${plain}"
                echo -e "  Standard SSH command:"
                echo -e "  ${yellow}ssh -L 2222:127.0.0.1:${panel_port} root@${server_ip}${plain}"
                echo -e "  If using an SSH key:"
                echo -e "  ${yellow}ssh -i <sshkeypath> -L 2222:127.0.0.1:${panel_port} root@${server_ip}${plain}"
                echo -e "  Then open in your browser:"
                echo -e "  ${yellow}http://localhost:2222/${web_base_path}${plain}"
                echo ""
                echo -e "${yellow}Alternative: point a reverse proxy (nginx/Caddy) at 127.0.0.1:${panel_port} and let it terminate TLS.${plain}"
            else
                echo -e "${yellow}Panel will listen on all interfaces over plain HTTP. Make sure something else is terminating TLS in front of it.${plain}"
            fi

            systemctl restart p-ui > /dev/null 2>&1
            echo -e "${green}✓ SSL setup skipped.${plain}"
            ;;
        *)
            echo -e "${red}Invalid option. Skipping SSL setup.${plain}"
            SSL_HOST="${server_ip}"
            ;;
    esac
}

config_after_install() {
    local existing_hasDefaultCredential=$(${pui_folder}/p-ui setting -show true | grep -Eo 'hasDefaultCredential: .+' | awk '{print $2}')
    local existing_webBasePath=$(${pui_folder}/p-ui setting -show true | grep -Eo 'webBasePath: .+' | awk '{print $2}' | sed 's#^/##')
    local existing_port=$(${pui_folder}/p-ui setting -show true | grep -Eo 'port: .+' | awk '{print $2}')
    # Properly detect empty cert by checking if cert: line exists and has content after it
    local existing_cert=$(${pui_folder}/p-ui setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    local URL_lists=(
        "https://api4.ipify.org"
        "https://ipv4.icanhazip.com"
        "https://v4.api.ipinfo.io/ip"
        "https://ipv4.myexternalip.com/raw"
        "https://4.ident.me"
        "https://check-host.net/ip"
    )
    local server_ip=""
    for ip_address in "${URL_lists[@]}"; do
        local response=$(curl -s -w "\n%{http_code}" --max-time 3 "${ip_address}" 2> /dev/null)
        local http_code=$(echo "$response" | tail -n1)
        local ip_result=$(echo "$response" | head -n-1 | tr -d '[:space:]"')
        if [[ "${http_code}" == "200" && "${ip_result}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            server_ip="${ip_result}"
            break
        fi
    done

    if [[ -z "$server_ip" ]]; then
        if [[ "$NONINTERACTIVE" == "1" ]]; then
            # Panel binds 0.0.0.0 regardless; the IP is only used to compose the
            # displayed access URL. Fall back to PUI_SERVER_IP or leave blank.
            server_ip="${PUI_SERVER_IP:-}"
        else
            echo -e "${yellow}Could not auto-detect server IP from any provider.${plain}"
            while [[ -z "$server_ip" ]]; do
                read -rp "Please enter your server's public IPv4 address: " server_ip
                server_ip="${server_ip// /}"
                if [[ ! "$server_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                    echo -e "${red}Invalid IPv4 address. Please try again.${plain}"
                    server_ip=""
                fi
            done
        fi
    fi

    if [[ ${#existing_webBasePath} -lt 4 ]]; then
        if [[ "$existing_hasDefaultCredential" == "true" ]]; then
            local config_webBasePath="${PUI_WEB_BASE_PATH:-$(gen_random_string 18)}"
            local config_username="${PUI_USERNAME:-$(gen_random_string 10)}"
            local config_password="${PUI_PASSWORD:-$(gen_random_string 10)}"
            local config_port=""

            if [[ "$NONINTERACTIVE" == "1" ]]; then
                if [[ -n "${PUI_PANEL_PORT:-}" ]]; then
                    config_port="${PUI_PANEL_PORT}"
                    echo -e "${yellow}Your Panel Port is: ${config_port}${plain}"
                else
                    config_port=$(shuf -i 1024-62000 -n 1)
                    echo -e "${yellow}Generated random port: ${config_port}${plain}"
                fi
            else
                read -rp "Would you like to customize the Panel Port settings? (If not, a random port will be applied) [y/n]: " config_confirm
                if [[ "${config_confirm}" == "y" || "${config_confirm}" == "Y" ]]; then
                    read -rp "Please set up the panel port: " config_port
                    echo -e "${yellow}Your Panel Port is: ${config_port}${plain}"
                else
                    config_port=$(shuf -i 1024-62000 -n 1)
                    echo -e "${yellow}Generated random port: ${config_port}${plain}"
                fi
            fi

            ${pui_folder}/p-ui setting -username "${config_username}" -password "${config_password}" -port "${config_port}" -webBasePath "${config_webBasePath}"

            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}     SSL Certificate Setup (RECOMMENDED)   ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}SSL is strongly recommended. Skip only if a reverse proxy${plain}"
            echo -e "${yellow}or SSH tunnel handles TLS for you.${plain}"
            echo -e "${yellow}Let's Encrypt now supports both domains and IP addresses!${plain}"
            echo ""

            prompt_and_setup_ssl "${config_port}" "${config_webBasePath}" "${server_ip}"

            # Retrieve the API token for display
            local config_apiToken=$(${pui_folder}/p-ui setting -getApiToken true | grep -Eo 'apiToken: .+' | awk '{print $2}')

            # Display final credentials and access information
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}     Panel Installation Complete!         ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}Username:    ${config_username}${plain}"
            echo -e "${green}Password:    ${config_password}${plain}"
            echo -e "${green}Port:        ${config_port}${plain}"
            echo -e "${green}WebBasePath: ${config_webBasePath}${plain}"
            echo -e "${green}Database:    ${DB_LABEL}${plain}"
            echo -e "${green}Access URL:  ${SSL_SCHEME}://${SSL_HOST}:${config_port}/${config_webBasePath}${plain}"
            echo -e "${green}API Token:   ${config_apiToken}${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}⚠ IMPORTANT: Save these credentials securely!${plain}"
            if [[ "$SSL_SCHEME" == "https" ]]; then
                echo -e "${yellow}⚠ SSL Certificate: Enabled and configured${plain}"
            else
                echo -e "${yellow}⚠ SSL Certificate: Skipped — panel is HTTP-only. Use a reverse proxy or SSH tunnel.${plain}"
            fi

            echo ""
            echo -e "${green}PostgreSQL backup & restore is built into the panel:${plain}"
            echo -e "  ${blue}${SSL_SCHEME}://${SSL_HOST}:${config_port}/${config_webBasePath}${plain} → Backup & Restore"
            echo -e "${yellow}  Back Up downloads a pg_dump .dump file; Restore reloads it via pg_restore.${plain}"

            if [[ "${PG_LOCAL_INSTALLED}" == "1" ]]; then
                echo ""
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${green}     PostgreSQL Credentials               ${plain}"
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${green}DB Name:    ${PG_DB}${plain}"
                echo -e "${green}Username:   ${PG_USER}${plain}"
                echo -e "${green}Password:   ${PG_PASS}${plain}"
                echo -e "${green}Host:       ${PG_HOST}${plain}"
                echo -e "${green}Port:       ${PG_PORT}${plain}"
                echo -e "${green}DSN:        ${PUI_DB_DSN}${plain}"
                echo -e "${green}Env file:   ${pui_env_file}${plain}"
                echo -e "${green}-------------------------------------------${plain}"
                echo -e "${green}Connect from this server:${plain}"
                echo -e "  ${blue}sudo -u postgres psql -d ${PG_DB}${plain}      (as the postgres superuser)"
                echo -e "  ${blue}PGPASSWORD='${PG_PASS}' psql -h ${PG_HOST} -p ${PG_PORT} -U ${PG_USER} -d ${PG_DB}${plain}"
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${yellow}⚠ The panel reads these credentials from ${pui_env_file}.${plain}"
                echo -e "${yellow}⚠ Save the password — it is not stored anywhere else in plain text.${plain}"
                unset PG_USER PG_PASS PG_HOST PG_PORT PG_DB
            fi

            # Persist a machine-parseable credentials file for cloud-init / MOTD.
            : "${SSL_SCHEME:=https}"
            : "${SSL_HOST:=${server_ip}}"
            write_install_result "${config_username}" "${config_password}" "${config_port}" \
                "${config_webBasePath}" "${SSL_SCHEME}" "${SSL_HOST}" "${config_apiToken}"
        else
            local config_webBasePath=$(gen_random_string 18)
            echo -e "${yellow}WebBasePath is missing or too short. Generating a new one...${plain}"
            ${pui_folder}/p-ui setting -webBasePath "${config_webBasePath}"
            echo -e "${green}New WebBasePath: ${config_webBasePath}${plain}"

            # If the panel is already installed but no certificate is configured, prompt for SSL now
            if [[ -z "${existing_cert}" ]]; then
                echo ""
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${green}     SSL Certificate Setup (RECOMMENDED)   ${plain}"
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${yellow}Let's Encrypt now supports both domains and IP addresses!${plain}"
                echo ""
                prompt_and_setup_ssl "${existing_port}" "${config_webBasePath}" "${server_ip}"
                echo -e "${green}Access URL:  ${SSL_SCHEME}://${SSL_HOST}:${existing_port}/${config_webBasePath}${plain}"
            else
                # If a cert already exists, just show the access URL
                echo -e "${green}Access URL: https://${server_ip}:${existing_port}/${config_webBasePath}${plain}"
            fi
        fi
    else
        if [[ "$existing_hasDefaultCredential" == "true" ]]; then
            local config_username="${PUI_USERNAME:-$(gen_random_string 10)}"
            local config_password="${PUI_PASSWORD:-$(gen_random_string 10)}"

            echo -e "${yellow}Default credentials detected. Security update required...${plain}"
            ${pui_folder}/p-ui setting -username "${config_username}" -password "${config_password}"
            echo -e "Generated new random login credentials:"
            echo -e "###############################################"
            echo -e "${green}Username: ${config_username}${plain}"
            echo -e "${green}Password: ${config_password}${plain}"
            echo -e "###############################################"

            # Persist a machine-parseable credentials file for cloud-init / MOTD.
            local config_apiToken
            config_apiToken=$(${pui_folder}/p-ui setting -getApiToken true | grep -Eo 'apiToken: .+' | awk '{print $2}')
            : "${SSL_SCHEME:=https}"
            : "${SSL_HOST:=${server_ip}}"
            write_install_result "${config_username}" "${config_password}" "${existing_port}" \
                "${existing_webBasePath}" "${SSL_SCHEME}" "${SSL_HOST}" "${config_apiToken}"
        else
            echo -e "${green}Username, Password, and WebBasePath are properly set.${plain}"
        fi

        # Existing install: if no cert configured, prompt user for SSL setup
        # Properly detect empty cert by checking if cert: line exists and has content after it
        existing_cert=$(${pui_folder}/p-ui setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
        if [[ -z "$existing_cert" ]]; then
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}     SSL Certificate Setup (RECOMMENDED)   ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}Let's Encrypt now supports both domains and IP addresses!${plain}"
            echo ""
            prompt_and_setup_ssl "${existing_port}" "${existing_webBasePath}" "${server_ip}"
            echo -e "${green}Access URL:  ${SSL_SCHEME}://${SSL_HOST}:${existing_port}/${existing_webBasePath}${plain}"
        else
            echo -e "${green}SSL certificate already configured. No action needed.${plain}"
        fi
    fi

    ${pui_folder}/p-ui migrate
}

# setup_fail2ban auto-installs and configures fail2ban for the IP Limit feature
# by invoking the freshly installed p-ui CLI. IP Limit is load-bearing on
# fail2ban (without it the panel disables the limitIp field and zeroes existing
# limits), so a fresh install should make it work out of the box. Non-fatal by
# design: a fail2ban failure must never abort the panel install.
setup_fail2ban() {
    if [[ -n "${PUI_ENABLE_FAIL2BAN+x}" && "${PUI_ENABLE_FAIL2BAN}" != "true" ]]; then
        echo -e "${yellow}PUI_ENABLE_FAIL2BAN=${PUI_ENABLE_FAIL2BAN}, skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    if [[ ! -x /usr/bin/p-ui ]]; then
        echo -e "${yellow}p-ui CLI not found; skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    echo -e "${green}Setting up Fail2ban for the IP Limit feature...${plain}"
    if /usr/bin/p-ui setup-fail2ban; then
        echo -e "${green}Fail2ban setup complete.${plain}"
    else
        echo -e "${yellow}Fail2ban setup did not finish; IP Limit stays disabled until you run 'p-ui' and open the IP Limit menu. Continuing.${plain}"
    fi
    return 0
}

# Lands a systemd unit file at ${pui_service}/p-ui.service via a temp file +
# atomic mv, so a failed cp/curl or an interrupted mv never leaves a
# truncated unit file at the live path -- systemd would then fail to parse
# it on the next daemon-reload/start. Same pattern already used for
# /usr/bin/p-ui elsewhere in this script. source_is_url picks cp (from a
# file already extracted from the release tarball) vs curl (GitHub fallback).
_install_pui_service_unit() {
    local source="$1"
    local source_is_url="$2"
    local dest="${pui_service}/p-ui.service"
    local temp_file="${dest}.tmp.$$"

    rm -f "$temp_file"
    if [[ "$source_is_url" == "true" ]]; then
        curl -fLRo "$temp_file" "$source" > /dev/null 2>&1
    else
        cp -f "$source" "$temp_file" > /dev/null 2>&1
    fi
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    if [[ ! -s "$temp_file" ]]; then
        rm -f "$temp_file"
        return 1
    fi
    mv -f "$temp_file" "$dest"
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    return 0
}

install_p-ui() {
    cd ${pui_folder%/p-ui}/

    # Testing hook: install from a locally built archive instead of a published
    # GitHub release. Point PUI_LOCAL_ARCHIVE at a p-ui-linux-<arch>.tar.gz built
    # the same way .github/workflows/release.yml builds it. This exists so the
    # install path can be exercised on a real host BEFORE a release is cut --
    # otherwise the very first run of this script is against production users.
    # Unset (the normal case) nothing below changes.
    if [[ -n "${PUI_LOCAL_ARCHIVE:-}" ]]; then
        if [[ ! -s "${PUI_LOCAL_ARCHIVE}" ]]; then
            echo -e "${red}PUI_LOCAL_ARCHIVE is set but '${PUI_LOCAL_ARCHIVE}' is missing or empty${plain}"
            exit 1
        fi
        tag_version="${1:-local}"
        echo -e "${yellow}Installing from local archive ${PUI_LOCAL_ARCHIVE} (version label: ${tag_version})${plain}"
        cp -f "${PUI_LOCAL_ARCHIVE}" "${pui_folder}-linux-$(arch).tar.gz"
    # Download resources
    elif [ $# == 0 ]; then
        tag_version=$(curl -Ls --retry 5 --retry-delay 3 --connect-timeout 15 --max-time 60 "https://api.github.com/repos/Arman2122/p-ui/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
        if [[ ! -n "$tag_version" ]]; then
            echo -e "${red}Failed to fetch p-ui version, it may be due to GitHub API restrictions, please try it later${plain}"
            exit 1
        fi
        echo -e "Got p-ui latest version: ${tag_version}, beginning the installation..."
        curl -fLR --retry 5 --retry-delay 3 --connect-timeout 15 --speed-limit 1 --speed-time 300 -o ${pui_folder}-linux-$(arch).tar.gz https://github.com/Arman2122/p-ui/releases/download/${tag_version}/p-ui-linux-$(arch).tar.gz
        if [[ $? -ne 0 ]]; then
            echo -e "${red}Downloading p-ui failed, please be sure that your server can access GitHub ${plain}"
            exit 1
        fi
        if [[ ! -s ${pui_folder}-linux-$(arch).tar.gz ]]; then
            rm ${pui_folder}-linux-$(arch).tar.gz -f
            echo -e "${red}Downloaded p-ui release archive is empty${plain}"
            exit 1
        fi
    else
        tag_version=$1
        # The rolling dev channel ships under a fixed, non-semver tag that is
        # force-moved to the latest main commit on every push. Accept `dev` as a
        # convenient alias and skip the numeric floor check for it.
        if [[ "$tag_version" == "dev" || "$tag_version" == "dev-latest" ]]; then
            tag_version="dev-latest"
            echo -e "${yellow}Installing the rolling dev build (tag: dev-latest). This is a per-commit pre-release, not a stable version.${plain}"
        else
            tag_version_numeric=${tag_version#v}
            # Penhoon UI's own release history starts at 1.0.0 -- this fork does
            # not continue upstream 3x-ui's tag line, so 1.0.0 is the oldest tag
            # that can possibly exist. The floor stays to reject bogus/ancient
            # tags (e.g. a copy-pasted upstream 0.x), it is just retargeted.
            min_version="1.0.0"

            if [[ "$(printf '%s\n' "$min_version" "$tag_version_numeric" | sort -V | head -n1)" != "$min_version" ]]; then
                echo -e "${red}Please use a newer version (at least v${min_version}). Exiting installation.${plain}"
                exit 1
            fi
        fi

        url="https://github.com/Arman2122/p-ui/releases/download/${tag_version}/p-ui-linux-$(arch).tar.gz"
        echo -e "Beginning to install p-ui ${tag_version}"
        curl -fLR --retry 5 --retry-delay 3 --connect-timeout 15 --speed-limit 1 --speed-time 300 -o ${pui_folder}-linux-$(arch).tar.gz ${url}
        if [[ $? -ne 0 ]]; then
            echo -e "${red}Download p-ui ${tag_version} failed, please check if the version exists ${plain}"
            exit 1
        fi
        if [[ ! -s ${pui_folder}-linux-$(arch).tar.gz ]]; then
            rm ${pui_folder}-linux-$(arch).tar.gz -f
            echo -e "${red}Downloaded p-ui release archive is empty${plain}"
            exit 1
        fi
    fi
    # The management script installed at /usr/bin/p-ui has to match the release
    # being installed -- it drives the panel binary and shares its assumptions.
    # It ships inside the archive, so take it from there. Fetching it from the
    # main branch instead (as this did) hands a pinned install such as
    # `install.sh v1.0.0` whatever script main happens to carry, and also
    # reaches out to GitHub when installing from PUI_LOCAL_ARCHIVE.
    # Archives predating the shipped script fall back to the main branch.
    # This runs BEFORE the old install is torn down, so a failure here is
    # non-destructive.
    local pui_script_temp="/usr/bin/p-ui-temp.$$"
    rm -f "${pui_script_temp}"
    if tar tzf ${pui_folder}-linux-$(arch).tar.gz p-ui/p-ui.sh >/dev/null 2>&1; then
        tar xzf ${pui_folder}-linux-$(arch).tar.gz -O p-ui/p-ui.sh >"${pui_script_temp}"
    else
        echo -e "${yellow}Release archive has no p-ui.sh; falling back to the main branch${plain}"
        curl -fLRo "${pui_script_temp}" https://raw.githubusercontent.com/Arman2122/p-ui/main/p-ui.sh
        if [[ $? -ne 0 ]]; then
            rm -f "${pui_script_temp}"
            echo -e "${red}Failed to download p-ui.sh${plain}"
            exit 1
        fi
    fi
    if [[ ! -s "${pui_script_temp}" ]]; then
        rm -f "${pui_script_temp}"
        echo -e "${red}p-ui.sh is missing or empty${plain}"
        exit 1
    fi

    # Stop p-ui service and remove old resources
    if [[ -e ${pui_folder}/ ]]; then
        systemctl stop p-ui
        # Kill any leftover mtg (MTProto) sidecars. p-ui runs them outside its own
        # lifecycle, so on Linux a stale one can survive the stop and keep holding
        # an inbound port with an outdated secret, silently breaking new clients.
        # The freshly installed panel respawns a clean mtg per inbound on start.
        pkill -f 'mtg-linux-[^ ]* run ' > /dev/null 2>&1 || true
        rm ${pui_folder}/ -rf
    fi

    # Extract resources and set permissions
    tar zxvf p-ui-linux-$(arch).tar.gz
    if [[ $? -ne 0 ]]; then
        rm p-ui-linux-$(arch).tar.gz -f
        rm -f "${pui_script_temp}"
        echo -e "${red}Failed to extract the p-ui release archive -- the previous installation has already been removed, so the panel will not start until this is fixed; try running the installer again${plain}"
        exit 1
    fi
    rm p-ui-linux-$(arch).tar.gz -f

    cd p-ui
    if [[ $? -ne 0 || ! -s p-ui ]]; then
        rm -f "${pui_script_temp}"
        echo -e "${red}Extracted p-ui archive is missing the p-ui binary -- the previous installation has already been removed, so the panel will not start until this is fixed; try running the installer again${plain}"
        exit 1
    fi
    chmod +x p-ui
    chmod +x p-ui.sh

    # Check the system's architecture and rename the file accordingly.
    # The panel binary maps GOARCH=arm to "arm32" (internal/xray/process.go),
    # so the Xray binary must be named xray-linux-arm32; mtg keeps plain "arm".
    if [[ $(arch) == "armv5" || $(arch) == "armv6" || $(arch) == "armv7" ]]; then
        mv bin/xray-linux-$(arch) bin/xray-linux-arm32
        chmod +x bin/xray-linux-arm32
        if [[ -f bin/mtg-linux-$(arch) ]]; then
            mv bin/mtg-linux-$(arch) bin/mtg-linux-arm
            chmod +x bin/mtg-linux-arm
        fi
    fi
    chmod +x p-ui bin/xray-linux-$(arch)
    if [[ -f bin/mtg-linux-arm ]]; then
        chmod +x bin/mtg-linux-arm
    elif [[ -f bin/mtg-linux-$(arch) ]]; then
        chmod +x bin/mtg-linux-$(arch)
    fi

    # Update p-ui cli and se set permission
    mv -f "${pui_script_temp}" /usr/bin/p-ui
    if [[ $? -ne 0 ]]; then
        rm -f "${pui_script_temp}"
        echo -e "${red}Failed to install p-ui.sh${plain}"
        exit 1
    fi
    chmod +x /usr/bin/p-ui
    mkdir -p /var/log/p-ui

    # PostgreSQL is mandatory and every `p-ui setting` call in
    # config_after_install needs a reachable database, so provision it first.
    setup_database
    config_after_install

    # Install systemd service file.
    # These paths are absolute on purpose: config_after_install above can run the
    # SSL setup, which shells out to acme.sh, and relative lookups here used to
    # resolve against whatever directory that left behind rather than the
    # extracted release -- silently falling through to the GitHub download below.
    service_installed=false

    if [ -f "${pui_folder}/p-ui.service" ]; then
        echo -e "${green}Found p-ui.service in extracted files, installing...${plain}"
        if _install_pui_service_unit "${pui_folder}/p-ui.service" "false"; then
            service_installed=true
        fi
    fi

    if [ "$service_installed" = false ] && [ -f "${pui_folder}/p-ui.service.debian" ]; then
        echo -e "${green}Found p-ui.service.debian in extracted files, installing...${plain}"
        if _install_pui_service_unit "${pui_folder}/p-ui.service.debian" "false"; then
            service_installed=true
        fi
    fi

    # If service file not found in tar.gz, download from GitHub
    if [ "$service_installed" = false ]; then
        echo -e "${yellow}Service files not found in tar.gz, downloading from GitHub...${plain}"
        if ! _install_pui_service_unit "https://raw.githubusercontent.com/Arman2122/p-ui/main/p-ui.service.debian" "true"; then
            echo -e "${red}Failed to install p-ui.service from GitHub${plain}"
            exit 1
        fi
        service_installed=true
    fi

    if [ "$service_installed" = true ]; then
        echo -e "${green}Setting up systemd unit...${plain}"
        chown root:root ${pui_service}/p-ui.service > /dev/null 2>&1
        chmod 644 ${pui_service}/p-ui.service > /dev/null 2>&1
        systemctl daemon-reload
        systemctl enable p-ui
        systemctl start p-ui
    else
        echo -e "${red}Failed to install p-ui.service file${plain}"
        exit 1
    fi

    # IP Limit relies on fail2ban; install + configure it now so the feature
    # works out of the box (no-op when PUI_ENABLE_FAIL2BAN=false). Never fatal.
    setup_fail2ban

    echo -e "${green}Penhoon UI ${tag_version}${plain} installation finished, it is running now..."
    echo -e ""
    echo -e "┌───────────────────────────────────────────────────────┐
│  ${blue}p-ui control menu usages (subcommands):${plain}              │
│                                                       │
│  ${blue}p-ui${plain}              - Admin Management Script          │
│  ${blue}p-ui start${plain}        - Start                            │
│  ${blue}p-ui stop${plain}         - Stop                             │
│  ${blue}p-ui restart${plain}      - Restart                          │
│  ${blue}p-ui status${plain}       - Current Status                   │
│  ${blue}p-ui settings${plain}     - Current Settings                 │
│  ${blue}p-ui enable${plain}       - Enable Autostart on OS Startup   │
│  ${blue}p-ui disable${plain}      - Disable Autostart on OS Startup  │
│  ${blue}p-ui log${plain}          - Check logs                       │
│  ${blue}p-ui banlog${plain}       - Check Fail2ban ban logs          │
│  ${blue}p-ui update${plain}       - Update                           │
│  ${blue}p-ui legacy${plain}       - Legacy version                   │
│  ${blue}p-ui install${plain}      - Install                          │
│  ${blue}p-ui uninstall${plain}    - Uninstall                        │
└───────────────────────────────────────────────────────┘"
}

echo -e "${green}Running...${plain}"
install_base
install_p-ui $1
