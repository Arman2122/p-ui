#!/usr/bin/env bash
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo 'Run the role menu as root.' >&2
    exit 1
fi
role=$(python3 -c 'import json; print(json.load(open("/etc/penhoon/role.json"))["role"])')
case "$role" in
    master|combined) unit=penhoon-master.service ;;
    node) unit=penhoon-agent.service ;;
    *) echo 'Invalid installation role.' >&2; exit 1 ;;
esac
command=${1:-}
if [[ -z "$command" ]]; then
    printf 'Penhoon role: %s\n1) Status\n2) Restart\n3) Logs\n4) Preflight\n' "$role"
    if [[ "$role" == node ]]; then
        echo 'These service commands manage the control agent. Runtime service and traffic changes are managed by the master.'
    fi
    read -r -p 'Select: ' command
fi
case "$command" in
    role) printf '%s\n' "$role" ;;
    1|status) systemctl status "$unit" --no-pager ;;
    2|restart) systemctl restart "$unit" ;;
    start|stop|enable|disable) systemctl "$command" "$unit" ;;
    3|log) journalctl -u "$unit" --no-pager -n 100 ;;
    4|preflight)
        if [[ "$role" == node ]]; then
            runuser -u penhoon-agent -- /usr/local/lib/penhoon/current/penhoon-agent check --runtimes /etc/penhoon/runtimes.json
        else
            exec python3 /usr/local/lib/penhoon-install/install.py --role "$role" --panel-command preflight
        fi
        ;;
    settings)
        if [[ "$role" == node ]]; then echo 'Node configuration is managed by its master.' >&2; exit 1; fi
        exec python3 /usr/local/lib/penhoon-install/install.py --role "$role" --panel-command setting -- "${@:2}"
        ;;
    *)
        echo 'Supported role commands: role, status, restart, start, stop, enable, disable, log, preflight, settings.' >&2
        echo 'Updates use the role installer and signed node updater. Role conversion and cross-role uninstall are refused.' >&2
        exit 1
        ;;
esac
