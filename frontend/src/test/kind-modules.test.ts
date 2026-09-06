import { describe, expect, it } from 'vitest';

import { PROTOCOL_VALUES } from '@/generated/zod';
import {
  clientSchemaFor,
  createDefaultInboundSettings,
  hasShareLink,
  isMultiUserKind,
  KIND_MODULES,
  kindModule,
} from '@/lib/cores/kinds';

/*
KIND_MODULES is a Record over Protocol and already fails to compile for a
missing kind; this holds the lookups to the same rule at runtime, and pins
what a kind this build does not know answers, since an older panel meets one
whenever a newer master created the inbound.
*/
describe('kind modules', () => {
  it('answers for every generated kind', () => {
    for (const kind of PROTOCOL_VALUES) {
      expect(kindModule(kind), kind).toBe(KIND_MODULES[kind]);
      expect(createDefaultInboundSettings(kind), `${kind} defaults`).not.toBeNull();
    }
    expect(Object.keys(KIND_MODULES).sort()).toEqual([...PROTOCOL_VALUES].sort());
  });

  it('treats an unknown kind as one with nothing to offer', () => {
    expect(kindModule('ftp')).toBeUndefined();
    expect(createDefaultInboundSettings('ftp')).toBeNull();
    expect(clientSchemaFor('ftp')).toBeNull();
    expect(isMultiUserKind('ftp', {})).toBe(false);
    expect(hasShareLink('ftp')).toBe(false);
  });

  it('carries a client list exactly where the kind has one', () => {
    for (const kind of PROTOCOL_VALUES) {
      const hasSchema = clientSchemaFor(kind) !== null;
      const multiUser = isMultiUserKind(kind, { method: '2022-blake3-aes-128-gcm' });
      expect(multiUser, `${kind}: client schema ${hasSchema}, multi-user ${multiUser}`).toBe(hasSchema);
    }
    expect(isMultiUserKind('shadowsocks', { method: '2022-blake3-chacha20-poly1305' })).toBe(false);
  });

  it('offers a share link only where the panel renders one', () => {
    const linked = PROTOCOL_VALUES.filter((kind) => hasShareLink(kind)).sort();
    expect(linked).toEqual(['hysteria', 'mtproto', 'shadowsocks', 'trojan', 'vless', 'vmess']);
  });
});
