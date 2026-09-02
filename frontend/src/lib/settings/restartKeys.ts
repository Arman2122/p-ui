import type { AllSetting } from '@/models/setting';

/* The settings the panel and the subscription server read only when they
   start: everything else applies on save. F402. */
export const RESTART_FIELD_LABELS: Readonly<Partial<Record<keyof AllSetting, string>>> = {
  webListen: 'pages.settings.panelListeningIP',
  webPort: 'pages.settings.panelPort',
  webBasePath: 'pages.settings.panelUrlPath',
  webCertFile: 'pages.settings.publicKeyPath',
  webKeyFile: 'pages.settings.privateKeyPath',
  sessionMaxAge: 'pages.settings.sessionMaxAge',
  subEnable: 'pages.settings.subEnable',
  subListen: 'pages.settings.subListen',
  subPort: 'pages.settings.subPort',
  subCertFile: 'pages.settings.subCertPath',
  subKeyFile: 'pages.settings.subKeyPath',
};

export type RestartKey = keyof typeof RESTART_FIELD_LABELS;

export const RESTART_KEYS = Object.keys(RESTART_FIELD_LABELS) as RestartKey[];

/** The restart-only settings whose saved value no longer matches what the running process loaded. */
export function restartPendingKeys(loaded: AllSetting | undefined, saved: AllSetting | undefined): RestartKey[] {
  if (!loaded || !saved) return [];
  return RESTART_KEYS.filter((key) => loaded[key] !== saved[key]);
}
