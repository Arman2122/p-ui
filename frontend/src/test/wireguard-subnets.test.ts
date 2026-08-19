import { describe, expect, it } from 'vitest';

import { takenSubnets, conflictingSubnet, suggestFreeSubnet } from '@/lib/xray/wireguard-subnets';

const inbound = (id: number, protocol: string, cidr: string, remark = '') => ({
  id, protocol, remark, settings: JSON.stringify({ address: [cidr] }),
});

/*
Two kernel devices on one subnet answer each other's clients.

Both install the same connected route into the one host routing table, so a
client of one is encrypted to the other's peer — traffic goes somewhere, to
somebody else's tunnel, and nothing fails loudly. The server refuses it on save;
this is the same question asked while the operator is still typing.
*/
describe('kernel WireGuard subnet conflicts', () => {
  const existing = [
    inbound(2, 'wgkernel', '10.77.0.1/24', 'wg-in'),
    inbound(3, 'awgkernel', '10.88.0.1/24', 'awg-in'),
    inbound(4, 'vless', '10.99.0.1/24', 'not a device'),
  ];

  it('counts only the protocols that own a host device', () => {
    const taken = takenSubnets(existing);
    expect(taken.map((t) => t.cidr).sort()).toEqual(['10.77.0.1/24', '10.88.0.1/24']);
  });

  it('catches an exact collision, whichever kernel core owns it', () => {
    const taken = takenSubnets(existing);
    expect(conflictingSubnet('10.77.0.1/24', taken)?.owner.id).toBe(2);
    expect(conflictingSubnet('10.88.0.5/24', taken)?.owner.id).toBe(3);
  });

  // Overlap, not equality: a /16 containing another inbound's /24 collides just
  // as completely, and equality alone would wave it through.
  it('catches a wider prefix that swallows another', () => {
    const taken = takenSubnets(existing);
    expect(conflictingSubnet('10.0.0.1/8', taken)).not.toBeNull();
    expect(conflictingSubnet('10.77.0.0/16', taken)?.owner.id).toBe(2);
  });

  it('allows a genuinely separate subnet', () => {
    expect(conflictingSubnet('10.90.0.1/24', takenSubnets(existing))).toBeNull();
  });

  // An inbound's own address is not a conflict with itself, or every edit of an
  // existing inbound would be refused.
  it('ignores the inbound being edited', () => {
    expect(conflictingSubnet('10.77.0.1/24', takenSubnets(existing, 2))).toBeNull();
  });

  it('handles IPv6 without treating it as v4', () => {
    const v6 = [inbound(5, 'wgkernel', 'fd00::1/64')];
    expect(conflictingSubnet('fd00::5/64', takenSubnets(v6))).not.toBeNull();
    expect(conflictingSubnet('fd01::1/64', takenSubnets(v6))).toBeNull();
    expect(conflictingSubnet('10.0.0.1/24', takenSubnets(v6))).toBeNull();
  });

  /* The default was 10.0.0.1/24 for every inbound: right for the first one and
     guaranteed to collide for the next. */
  it('suggests a free subnet rather than one already served', () => {
    const suggestion = suggestFreeSubnet(takenSubnets(existing));
    expect(conflictingSubnet(suggestion, takenSubnets(existing))).toBeNull();
    expect(suggestion).toBe('10.0.0.1/24');

    const crowded = takenSubnets([inbound(1, 'wgkernel', '10.0.0.1/24'), ...existing]);
    const next = suggestFreeSubnet(crowded);
    expect(conflictingSubnet(next, crowded)).toBeNull();
    expect(next).toBe('10.1.0.1/24');
  });
});
