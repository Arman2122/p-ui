import { describe, it, expect } from 'vitest';

import { outboundProtocolGroups, registryExitKinds } from '@/lib/cores/outbound-protocols';
import type { CoreView } from '@/generated/zod';

function core(over: Partial<CoreView>): CoreView {
  return {
    id: 'x',
    titleKey: 'cores.x.title',
    kinds: [],
    caps: {},
    exitKinds: [],
    clientCredentials: {},
    shaping: {},
    available: true,
    unavailable: '',
    ...over,
  };
}

const XRAY = core({ id: 'xray', titleKey: 'cores.xray.title', kinds: ['vless', 'vmess'] });
const WIREGUARD = core({
  id: 'wireguard',
  titleKey: 'cores.wireguard.title',
  kinds: ['wgkernel'],
  exitKinds: ['wgkernel'],
});
const MTPROTO = core({ id: 'mtproto', titleKey: 'cores.mtproto.title', kinds: ['mtproto'] });

const BUILTIN = { coreId: 'xray', kinds: ['vless', 'wireguard', 'freedom'] as const };

describe('registryExitKinds', () => {
  it('reports only kinds a route can terminate on, with the core that owns them', () => {
    expect(registryExitKinds([XRAY, WIREGUARD, MTPROTO])).toEqual([
      { kind: 'wgkernel', coreId: 'wireguard', unavailable: '' },
    ]);
  });

  it('carries the reason a blocked core cannot be used, rather than dropping it', () => {
    const blocked = core({
      ...WIREGUARD,
      available: false,
      unavailable: 'wireguard: no kernel support on this host',
    });
    expect(registryExitKinds([blocked])).toEqual([
      { kind: 'wgkernel', coreId: 'wireguard', unavailable: 'wireguard: no kernel support on this host' },
    ]);
  });

  it('treats a missing manifest as nothing to add, never as a crash', () => {
    expect(registryExitKinds(undefined)).toEqual([]);
  });
});

describe('outboundProtocolGroups', () => {
  it("puts the host core's own protocols first, then each other core's exits", () => {
    const groups = outboundProtocolGroups([WIREGUARD, XRAY, MTPROTO], BUILTIN);
    expect(groups.map((g) => g.coreId)).toEqual(['xray', 'wireguard']);
    expect(groups[0].titleKey).toBe('cores.xray.title');
    expect(groups[0].options.map((o) => o.kind)).toEqual(['vless', 'wireguard', 'freedom']);
    expect(groups[1].options.map((o) => o.kind)).toEqual(['wgkernel']);
  });

  it("keeps Xray's userspace wireguard outbound apart from the wgkernel core", () => {
    const groups = outboundProtocolGroups([XRAY, WIREGUARD], BUILTIN);
    const owners = groups.flatMap((g) => g.options.map((o) => `${g.coreId}:${o.kind}`));
    expect(owners).toContain('xray:wireguard');
    expect(owners).toContain('wireguard:wgkernel');
  });

  it('never lists one kind twice when a core also declares a built-in name', () => {
    const overlapping = core({ id: 'other', titleKey: 'cores.other.title', exitKinds: ['freedom', 'ovpn'] });
    const groups = outboundProtocolGroups([XRAY, overlapping], BUILTIN);
    const kinds = groups.flatMap((g) => g.options.map((o) => o.kind));
    expect(kinds.filter((k) => k === 'freedom')).toHaveLength(1);
    expect(kinds).toContain('ovpn');
  });

  it('offers the built-in protocols even when the registry is unreachable', () => {
    const groups = outboundProtocolGroups(undefined, BUILTIN);
    expect(groups).toHaveLength(1);
    expect(groups[0].options.map((o) => o.kind)).toEqual(['vless', 'wireguard', 'freedom']);
    expect(groups[0].titleKey).toBe('cores.xray.title');
  });

  it('a core offering no exit contributes no group at all', () => {
    const groups = outboundProtocolGroups([XRAY, MTPROTO], BUILTIN);
    expect(groups.map((g) => g.coreId)).toEqual(['xray']);
  });
});
