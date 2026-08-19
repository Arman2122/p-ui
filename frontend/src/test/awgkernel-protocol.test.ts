import { describe, expect, it } from 'vitest';

import { InboundSettingsSchema } from '@/schemas/protocols/inbound';
import { isKernelWireguard, isWireguardLike } from '@/lib/xray/kernel-wireguard';
import { createDefaultInboundSettings } from '@/lib/xray/inbound-defaults';

/*
Creating an AmneziaWG inbound must be possible at all.

It was not: the protocol was registered in Go, offered in the picker, and then
refused by this schema with "protocol — Invalid input", because the discriminated
union had no branch for it. Nothing else caught that — the Go side was complete
and every Go test passed.
*/
describe('an awgkernel inbound', () => {
  it('is accepted by the inbound schema', () => {
    const parsed = InboundSettingsSchema.safeParse({
      protocol: 'awgkernel',
      settings: {
        address: ['10.88.0.1/24'],
        secretKey: 'iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=',
        clients: [],
        awg: { jc: 4, jmin: 40, jmax: 70 },
      },
    });
    expect(parsed.success, JSON.stringify(parsed.error?.issues)).toBe(true);
  });

  // The obfuscation is optional: an inbound with none of it is plain WireGuard
  // carried by the AmneziaWG module, which is a legitimate thing to run.
  it('is accepted without any obfuscation', () => {
    const parsed = InboundSettingsSchema.safeParse({
      protocol: 'awgkernel',
      settings: {
        address: ['10.88.0.1/24'],
        secretKey: 'iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=',
        clients: [],
      },
    });
    expect(parsed.success).toBe(true);
  });

  it('has default settings, so the form opens with a usable device', () => {
    expect(() => createDefaultInboundSettings('awgkernel')).not.toThrow();
  });
});

/*
Every question the UI asks about a wgkernel inbound has the same answer for an
AmneziaWG one, because it is the same device with obfuscation added.

Asking it as one predicate is the point: the same comparison was repeated at
thirteen sites, and a protocol added to twelve of them is an inbound that half
the UI refuses to render.
*/
describe('the kernel WireGuard predicates', () => {
  it('cover both kernel protocols', () => {
    for (const protocol of ['wgkernel', 'awgkernel']) {
      expect(isKernelWireguard(protocol), protocol).toBe(true);
      expect(isWireguardLike(protocol), protocol).toBe(true);
    }
  });

  it("treats xray's userspace tunnel as WireGuard but not as a kernel device", () => {
    expect(isWireguardLike('wireguard')).toBe(true);
    expect(isKernelWireguard('wireguard')).toBe(false);
  });

  it('says no to everything else, including nothing at all', () => {
    for (const protocol of ['vless', 'mtproto', '', undefined, null]) {
      expect(isWireguardLike(protocol as string), String(protocol)).toBe(false);
    }
  });
});
