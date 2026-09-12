export function backupRestoreCommand(origin: string, basePath: string): string {
  const endpoint = `${origin}${basePath.replace(/\/$/, '')}/panel/api/backups/upload`;
  return `python3 - <<'PUI_RESTORE'
import getpass, json, subprocess, sys
sys.stdin = open('/dev/tty')
archive = input('Backup file (.dump or .dump.pui): ').strip()
token = getpass.getpass('Administrator API token: ')
passphrase = getpass.getpass('Archive passphrase (blank for unencrypted): ')
if input('Type RESTORE to replace the panel database: ') != 'RESTORE':
    raise SystemExit('Cancelled')
quote = lambda value: json.dumps(value, ensure_ascii=False)
upload = 'file=@"' + archive.replace('\\\\', '\\\\\\\\').replace('"', '\\\\"') + '"'
config = '\\n'.join([
    'header = ' + quote('Authorization: Bearer ' + token),
    'form = ' + quote(upload),
    'form-string = ' + quote('confirm=RESTORE'),
    'form-string = ' + quote('passphrase=' + passphrase),
])
result = subprocess.run(['curl', '--silent', '--show-error', '--fail-with-body', '--config', '-', ${JSON.stringify(endpoint)}], input=config.encode(), stdout=subprocess.PIPE)
sys.stdout.buffer.write(result.stdout)
if result.returncode:
    raise SystemExit(result.returncode)
try:
    response = json.loads(result.stdout)
except (ValueError, TypeError):
    raise SystemExit('Panel returned an invalid response')
raise SystemExit(0 if response.get('success') is True else 1)
PUI_RESTORE`;
}
