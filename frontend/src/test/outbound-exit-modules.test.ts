import { describe, it, expect } from 'vitest';

import {
  authorableExitKinds,
  exitModuleForKind,
  exitModuleForType,
} from '@/pages/xray/outbounds/exits';
import { EgressFormFields, type EgressRecord } from '@/schemas/api/egress';

/*
Before this registry both halves were the WireGuard uplink's: any kind a core
declared rendered WireGuard's fields and saved itself as a wg-client, so a second
exit core would have written a row the wrong driver then tried to dial.
*/
describe('an exit kind resolves to its own module', () => {
  it('maps the core kind the picker shows to the row type the driver claims', () => {
    const module = exitModuleForKind('wgkernel');
    expect(module).toBeDefined();
    expect(module!.egressType).toBe('wg-client');
  });

  it('has no module for a kind this build cannot author, rather than a wrong one', () => {
    /* The registry is the backend's answer about what CAN exit; this build
       either has a form or says so. Silently reusing another kind's is the bug. */
    expect(exitModuleForKind('openvpn')).toBeUndefined();
    expect(exitModuleForKind('')).toBeUndefined();
    expect(authorableExitKinds()).toEqual(['wgkernel']);
  });

  it('finds the module for a stored row by its type, since a row carries no kind', () => {
    expect(exitModuleForType('wg-client')).toBe(exitModuleForKind('wgkernel'));
    expect(exitModuleForType('xray-tun')).toBeUndefined();
    expect(exitModuleForType('openvpn-client')).toBeUndefined();
  });
});

describe('the wgkernel module round-trips a row', () => {
  const row: EgressRecord = {
    id: 4,
    type: 'wg-client',
    enable: true,
    remark: 'mullvad',
    target: '',
    settings: JSON.stringify({
      privateKey: 'priv', publicKey: 'pub', endpoint: 'us.example.com:51820',
      presharedKey: 'psk', address: ['10.14.0.2/32', 'fc00::2/128'], mtu: 1420, keepAlive: 25,
    }),
    createdAt: 0,
    updatedAt: 0,
  } as EgressRecord;

  it('reads a stored row back into the form it was written from', () => {
    const form = exitModuleForKind('wgkernel')!.fromRecord(row);
    expect(form.endpoint).toBe('us.example.com:51820');
    expect(form.remark).toBe('mullvad');
    /* One address per line is how a provider's .conf writes it, so pasting one
       whole works and editing does not reformat what the operator pasted. */
    expect(form.address).toBe('10.14.0.2/32\nfc00::2/128');
  });

  it('writes the form back to the same row shape', () => {
    const module = exitModuleForKind('wgkernel')!;
    const payload = module.toPayload(module.fromRecord(row), 'mullvad');
    expect(payload.type).toBe(module.egressType);
    expect(JSON.parse(payload.settings)).toMatchObject({
      endpoint: 'us.example.com:51820',
      address: ['10.14.0.2/32', 'fc00::2/128'],
      mtu: 1420,
    });
  });

  it('opens an empty form for settings it cannot read, rather than throwing', () => {
    const broken = { ...row, settings: '{not json' } as EgressRecord;
    const form = exitModuleForKind('wgkernel')!.fromRecord(broken);
    expect(form.endpoint).toBe('');
    // The remark still survives: it is a column, not part of the unreadable blob.
    expect(form.remark).toBe('mullvad');
    expect(() => EgressFormFields.parse(form)).not.toThrow();
  });
});
