// Pure builders for the p-ui install command. No React/DOM.

export interface InstallOptions {
  /** A release tag like `v3.4.1`, or empty/`latest` for the latest release. */
  version: string;
  /** Install with zero prompts (PUI_NONINTERACTIVE=1). */
  unattended: boolean;
  enableFail2ban: boolean;
  /** Honoured on unattended installs only; otherwise the installer asks. */
  panelPort: string;
  webBasePath: string;
  /** Point the panel at an existing PostgreSQL server instead of installing one. */
  dbDsn: string;
}

const REPO_RAW = 'https://raw.githubusercontent.com/Arman2122/p-ui/main/install.sh';

function isLatest(version: string): boolean {
  const v = version.trim().toLowerCase();
  return v === '' || v === 'latest';
}

/**
 * The one-line script install command. The install script on `main` reads the
 * version as its first argument: empty = latest stable release, a tag like
 * `v3.4.0` = that release, and `dev-latest` = the rolling per-commit dev build.
 */
export function buildScriptCommand(options: InstallOptions): string {
  if (isLatest(options.version)) {
    return `bash <(curl -Ls ${REPO_RAW})`;
  }
  return `bash <(curl -Ls ${REPO_RAW}) ${options.version.trim()}`;
}

/** The `PUI_*` overrides the installer reads from the environment. */
export function buildEnvAssignments(options: InstallOptions): string[] {
  const env: string[] = [];
  if (options.unattended) env.push('PUI_NONINTERACTIVE=1');
  if (!options.enableFail2ban) env.push('PUI_ENABLE_FAIL2BAN=false');
  if (options.panelPort.trim()) env.push(`PUI_PANEL_PORT=${options.panelPort.trim()}`);
  if (options.webBasePath.trim()) env.push(`PUI_WEB_BASE_PATH=${options.webBasePath.trim()}`);
  if (options.dbDsn.trim()) env.push(`PUI_DB_DSN='${options.dbDsn.trim()}'`);
  return env;
}

/** The full command: environment overrides followed by the install script. */
export function buildInstallCommand(options: InstallOptions): string {
  const env = buildEnvAssignments(options);
  const script = buildScriptCommand(options);
  return env.length === 0 ? script : `${env.join(' ')} ${script}`;
}
