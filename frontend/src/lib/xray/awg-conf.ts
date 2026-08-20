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
