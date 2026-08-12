import { describe, it, expect } from 'vitest';

import { wireguardPoolUsage } from '@/lib/xray/wireguard-pool';

function clients(...addrs: string[]) {
  return addrs.map((a) => ({ allowedIPs: [a] }));
}

/*
The numbers have to match the Go allocator, or the form advises a size the
server will not honour. Capacity is its own candidate range: it starts at .2 and
runs while the scope contains the address, so a /24 offers .2 through .255.
*/
describe('wireguardPoolUsage', () => {
  it('sizes the prefix the way the allocator walks it', () => {
    const cases: Array<[string, number]> = [
      ['10.0.0.1/24', 254],
      ['10.90.4.1/22', 1022],
      ['10.90.0.1/16', 65534],
      ['10.0.0.1/32', 0],
    ];
    for (const [address, capacity] of cases) {
      expect(wireguardPoolUsage([address], [])?.capacity, address).toBe(capacity);
    }
  });

  it('reports the network, not the address the device answers on', () => {
    expect(wireguardPoolUsage(['10.90.4.9/22'], [])?.prefix).toBe('10.90.4.0/22');
  });

  it('counts each allocated address once', () => {
    const usage = wireguardPoolUsage(['10.0.0.1/24'], clients('10.0.0.2/32', '10.0.0.3/32'));
    expect(usage).toMatchObject({ used: 2, outside: 0 });
  });

  /* A client already outside the prefix is the thing worth saying: each one is a
     per-peer kernel route the reconciler diffs on every pass. */
  it('separates the clients that already spilled out of the prefix', () => {
    const usage = wireguardPoolUsage(['10.90.4.1/22'], clients('10.90.4.2/32', '10.90.0.7/32'));
    expect(usage).toMatchObject({ used: 1, outside: 1 });
  });

  /* A catch-all or a site-to-site route is a real configuration and not an
     allocation; the allocator keys what is taken on the address alone. */
  it('ignores anything that is not a single-address allocation', () => {
    const usage = wireguardPoolUsage(['10.0.0.1/24'], [
      { allowedIPs: ['0.0.0.0/0', '::/0', '192.168.50.0/24', '10.0.0.2/32'] },
    ]);
    expect(usage).toMatchObject({ used: 1, outside: 0 });
  });

  it('answers nothing when there is no IPv4 device address to size', () => {
    expect(wireguardPoolUsage(['fd00::1/64'], [])).toBeNull();
    expect(wireguardPoolUsage([], [])).toBeNull();
    expect(wireguardPoolUsage(undefined, undefined)).toBeNull();
    expect(wireguardPoolUsage(['not-an-address'], [])).toBeNull();
  });

  /* A /0 would make the mask shift by 32, which is a no-op in JS rather than a
     zero — the trap this helper special-cases. */
  it('survives a zero-bit prefix', () => {
    expect(wireguardPoolUsage(['0.0.0.0/0'], clients('10.0.0.2/32'))).toMatchObject({
      prefix: '0.0.0.0/0',
      used: 1,
      outside: 0,
    });
  });
});
