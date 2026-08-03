'use client';

import { useState } from 'react';
import { buildInstallCommand, type InstallOptions } from '@/lib/xray/install';
import { ToolFrame } from './tool-frame';
import { TextField, CheckboxField } from './shared/fields';
import { OutputBlock } from './shared/output-block';

export function InstallCommandBuilder() {
  const [version, setVersion] = useState('');
  const [unattended, setUnattended] = useState(false);
  const [enableFail2ban, setEnableFail2ban] = useState(true);
  const [panelPort, setPanelPort] = useState('');
  const [webBasePath, setWebBasePath] = useState('');
  const [dbDsn, setDbDsn] = useState('');

  const options: InstallOptions = {
    version,
    unattended,
    enableFail2ban,
    panelPort,
    webBasePath,
    dbDsn,
  };

  return (
    <ToolFrame
      title="Install command builder"
      description="Build the exact install command for your Ubuntu or Debian server. It is assembled in your browser."
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <TextField
          label="Version"
          value={version}
          onChange={setVersion}
          placeholder="latest"
          hint="blank = latest stable · a tag like v3.4.0 · or dev-latest for the rolling dev build"
        />
        <TextField
          label="Web base path"
          value={webBasePath}
          onChange={setWebBasePath}
          placeholder="random"
          hint="blank = the installer generates a random path"
        />
        <TextField
          label="Panel port"
          value={panelPort}
          onChange={setPanelPort}
          placeholder="random"
          inputMode="numeric"
          hint="unattended installs only — otherwise the installer asks"
        />
        <TextField
          label="PostgreSQL DSN"
          value={dbDsn}
          onChange={setDbDsn}
          placeholder="postgres://user:pass@host:5432/dbname?sslmode=disable"
          hint="blank = the installer installs PostgreSQL locally"
        />
      </div>

      <div className="mt-3 flex flex-wrap gap-4">
        <CheckboxField
          label="Unattended (no prompts)"
          checked={unattended}
          onChange={setUnattended}
        />
        <CheckboxField
          label="Enable Fail2ban"
          checked={enableFail2ban}
          onChange={setEnableFail2ban}
        />
      </div>

      <div className="mt-4 grid grid-cols-1 gap-4">
        <OutputBlock label="Run on your server" value={buildInstallCommand(options)} />
      </div>
    </ToolFrame>
  );
}
