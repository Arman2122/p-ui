import { describe, it, expect } from 'vitest';
import {
  buildScriptCommand,
  buildEnvAssignments,
  buildInstallCommand,
  type InstallOptions,
} from './install';

const base: InstallOptions = {
  version: '',
  unattended: false,
  enableFail2ban: true,
  panelPort: '',
  webBasePath: '',
  dbDsn: '',
};

describe('buildScriptCommand', () => {
  it('uses the main-branch install.sh for the latest version', () => {
    expect(buildScriptCommand(base)).toBe(
      'bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)',
    );
  });

  it('pins a specific version by passing the tag to main-branch install.sh', () => {
    const cmd = buildScriptCommand({ ...base, version: 'v3.4.1' });
    expect(cmd).toBe(
      'bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) v3.4.1',
    );
  });

  it('supports the rolling dev-latest build', () => {
    const cmd = buildScriptCommand({ ...base, version: 'dev-latest' });
    expect(cmd).toBe(
      'bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh) dev-latest',
    );
  });
});

describe('buildEnvAssignments', () => {
  it('emits nothing for the interactive defaults', () => {
    expect(buildEnvAssignments(base)).toEqual([]);
  });

  it('emits the overrides the installer reads', () => {
    const env = buildEnvAssignments({
      ...base,
      unattended: true,
      enableFail2ban: false,
      panelPort: '8443',
      webBasePath: 'panel',
      dbDsn: 'postgres://pui:pw@db:5432/pui?sslmode=disable',
    });
    expect(env).toEqual([
      'PUI_NONINTERACTIVE=1',
      'PUI_ENABLE_FAIL2BAN=false',
      'PUI_PANEL_PORT=8443',
      'PUI_WEB_BASE_PATH=panel',
      "PUI_DB_DSN='postgres://pui:pw@db:5432/pui?sslmode=disable'",
    ]);
  });
});

describe('buildInstallCommand', () => {
  it('is the bare script when nothing is overridden', () => {
    expect(buildInstallCommand(base)).toBe(buildScriptCommand(base));
  });

  it('prefixes the overrides before the script', () => {
    const cmd = buildInstallCommand({ ...base, unattended: true, panelPort: '2096' });
    expect(cmd).toBe(
      'PUI_NONINTERACTIVE=1 PUI_PANEL_PORT=2096 bash <(curl -Ls https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh)',
    );
  });
});
