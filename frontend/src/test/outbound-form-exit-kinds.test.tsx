import { describe, it, expect } from 'vitest';

import { EgressFormFields, uplinkPayload } from '@/schemas/api/egress';
import { outboundProtocolGroups } from '@/lib/cores/outbound-protocols';
import { PROTOCOL_NAMES } from '@/pages/xray/outbounds/outbound-form-constants';
import type { CoreView } from '@/generated/zod';

const XRAY_CORE = {
  id: 'xray',
  titleKey: 'cores.xray.title',
  kinds: ['vless'],
  caps: {},
  exitKinds: [] as string[],
  clientCredentials: {},
  shaping: {},
  available: true,
  unavailable: '',
} as CoreView;

const WIREGUARD_CORE = {
  ...XRAY_CORE,
  id: 'wireguard',
  titleKey: 'cores.wgkernel.title',
  kinds: ['wgkernel'],
  exitKinds: ['wgkernel'],
} as CoreView;

/* The modal branches on exactly this: a kind some core declared as an exit is
   a database row, anything else is a line in the Xray config. */
function exitKindsOffered(cores: CoreView[]): Set<string> {
  const groups = outboundProtocolGroups(cores, { coreId: 'xray', kinds: PROTOCOL_NAMES });
  return new Set(
    groups.filter((g) => g.coreId !== 'xray').flatMap((g) => g.options.map((o) => o.kind)),
  );
}

describe('which outbound kinds the modal treats as an exit', () => {
  it('treats a core-declared kind as an exit and leaves Xray protocols alone', () => {
    const offered = exitKindsOffered([XRAY_CORE, WIREGUARD_CORE]);
    expect(offered.has('wgkernel')).toBe(true);
    /* Xray ships a userspace `wireguard` OUTBOUND. It must stay an Xray
       outbound, or picking it would post an egress row instead. */
    expect(offered.has('wireguard')).toBe(false);
    expect(offered.has('vless')).toBe(false);
  });

  it('treats nothing as an exit when no core declares one', () => {
    expect(exitKindsOffered([XRAY_CORE]).size).toBe(0);
  });

  it('treats nothing as an exit when the registry cannot be reached', () => {
    const groups = outboundProtocolGroups(undefined, { coreId: 'xray', kinds: PROTOCOL_NAMES });
    expect(groups.filter((g) => g.coreId !== 'xray')).toHaveLength(0);
  });
});

describe('the payload an exit is saved as', () => {
  const form = EgressFormFields.parse({
    endpoint: 'us-sfo.example.com:51820',
    privateKey: 'priv',
    publicKey: 'pub',
    address: '10.14.0.2/32, fc00::2/128',
    presharedKey: 'psk',
    mtu: 1420,
    keepAlive: 25,
  });

  it('carries the credentials as driver settings and names no target', () => {
    const payload = uplinkPayload(form, '  mullvad  ');
    expect(payload.type).toBe('wg-client');
    /* An uplink IS the destination; sending a target would make it look like a
       front and the backend refuses it. */
    expect(payload.target).toBe('');
    expect(payload.owner).toBe('operator');
    expect(payload.remark).toBe('mullvad');
    expect(JSON.parse(payload.settings)).toEqual({
      privateKey: 'priv',
      publicKey: 'pub',
      endpoint: 'us-sfo.example.com:51820',
      presharedKey: 'psk',
      address: ['10.14.0.2/32', 'fc00::2/128'],
      mtu: 1420,
      keepAlive: 25,
    });
  });

  it('splits a pasted address block the way a provider .conf writes it', () => {
    const payload = uplinkPayload(EgressFormFields.parse({ address: '10.0.0.2/32\n10.0.0.3/32' }), 'x');
    expect(JSON.parse(payload.settings).address).toEqual(['10.0.0.2/32', '10.0.0.3/32']);
  });
});
