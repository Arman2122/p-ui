import type { AwgParams } from '@/schemas/protocols/inbound/awgkernel';

/*
The obfuscation lines an AmneziaWG client needs in its [Interface] block.

The panel builds a .conf in two places -- here for the QR and download panel,
and in Go for the subscription -- and both must emit these. A config handed out
without them describes a tunnel that completes NO handshake against an
obfuscated server: not slower, not degraded, just silent on both sides.

Written as awg-tools reads them: a range is "lo-hi" and collapses to a bare
number when the bounds match, which their parser expands back to [n, n].
*/
export function awgInterfaceLines(params: AwgParams | undefined): string {
  if (!params) return '';

  const out: string[] = [];
  const num = (key: string, value: number | undefined) => {
    if (typeof value === 'number' && value > 0) out.push(`${key} = ${value}`);
  };
  const text = (key: string, value: string | undefined) => {
    if (value) out.push(`${key} = ${value}`);
  };

  num('Jc', params.jc);
  num('Jmin', params.jmin);
  num('Jmax', params.jmax);
  num('S1', params.s1);
  num('S2', params.s2);
  num('S3', params.s3);
  num('S4', params.s4);
  text('I1', params.i1);
  text('I2', params.i2);
  text('I3', params.i3);
  text('I4', params.i4);
  text('I5', params.i5);
  text('HeaderProtectionKey', params.headerProtectionKey);
  if (params.randomTrailers) out.push('RandomTrailers = on');
  if (params.disableCookies) out.push('DisableCookies = on');

  return out.length > 0 ? `${out.join('\n')}\n` : '';
}

/* The link keys the panel writes for an AmneziaWG inbound, paired with the
   .conf key each becomes. Lower-cased in the link like every other key there. */
const LINK_TO_CONF: [string, string][] = [
  ['jc', 'Jc'], ['jmin', 'Jmin'], ['jmax', 'Jmax'],
  ['s1', 'S1'], ['s2', 'S2'], ['s3', 'S3'], ['s4', 'S4'],
  ['h1', 'H1'], ['h2', 'H2'], ['h3', 'H3'], ['h4', 'H4'],
  ['i1', 'I1'], ['i2', 'I2'], ['i3', 'I3'], ['i4', 'I4'], ['i5', 'I5'],
  ['headerprotectionkey', 'HeaderProtectionKey'],
  ['contentpaddingaddition', 'ContentPaddingAddition'],
  ['rekeyaftertime', 'RekeyAfterTime'], ['rekeytimeout', 'RekeyTimeout'],
  ['rejectaftertime', 'RejectAfterTime'], ['keepalivetimeout', 'KeepaliveTimeout'],
  ['maxhandshakeattempts', 'MaxHandshakeAttempts'],
  ['randomtrailers', 'RandomTrailers'], ['disablecookies', 'DisableCookies'],
];

/*
The obfuscation carried in a share link, as [Interface] lines.

The subscription page builds its downloadable config from the LINK rather than
from the inbound, so a link that drops these produces a config that cannot
connect to the server that issued it — which is exactly what happened before the
link carried them.
*/
export function awgLinesFromLinkParams(params: URLSearchParams): string[] {
  const out: string[] = [];
  for (const [linkKey, confKey] of LINK_TO_CONF) {
    const value = params.get(linkKey);
    if (value) out.push(`${confKey} = ${value}`);
  }
  return out;
}

/* Whether a link describes an obfuscated tunnel, which is the only way to tell
   an AmneziaWG share link from a WireGuard one: both use wireguard://. */
export function linkCarriesObfuscation(params: URLSearchParams): boolean {
  return LINK_TO_CONF.some(([linkKey]) => Boolean(params.get(linkKey)));
}

/* The link params for an inbound's obfuscation, so a link the panel builds
   carries what the config built from it will need. */
export function awgLinkParams(params: AwgParams | undefined): [string, string][] {
  if (!params) return [];
  const record = params as unknown as Record<string, unknown>;
  const out: [string, string][] = [];
  for (const [linkKey, confKey] of LINK_TO_CONF) {
    const value = record[confKey.charAt(0).toLowerCase() + confKey.slice(1)];
    if (typeof value === 'number' && value > 0) out.push([linkKey, String(value)]);
    else if (typeof value === 'string' && value !== '') out.push([linkKey, value]);
    else if (value === true) out.push([linkKey, 'on']);
  }
  return out;
}
