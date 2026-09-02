/*
Every AmneziaWG obfuscation value is baked into each client's config, so an
edit that changes one silently cuts every client off until it is re-issued.
This names whether a save changes any of them, for the confirmation the form
shows before such a save.
*/
const AWG_OBFUSCATION_KEYS = [
  'jc', 'jmin', 'jmax', 's1', 's2', 's3', 's4',
  'h1', 'h2', 'h3', 'h4',
  'i1', 'i2', 'i3', 'i4', 'i5',
  'headerProtectionKey', 'randomTrailers', 'disableCookies',
  'contentPaddingAddition', 'rekeyAfterTime', 'rekeyTimeout', 'rejectAfterTime',
  'keepaliveTimeout', 'maxHandshakeAttempts',
] as const;

type AwgCarrier = { settings?: { awg?: Record<string, unknown> } | null } | null | undefined;

function awgOf(value: unknown): Record<string, unknown> {
  const awg = (value as AwgCarrier)?.settings?.awg;
  return awg && typeof awg === 'object' ? awg : {};
}

function normalise(value: unknown): string {
  if (value === undefined || value === null || value === '' || value === false) return '';
  return String(value).trim();
}

export function awgObfuscationChanged(before: unknown, after: unknown): boolean {
  const a = awgOf(before);
  const b = awgOf(after);
  return AWG_OBFUSCATION_KEYS.some((key) => normalise(a[key]) !== normalise(b[key]));
}
