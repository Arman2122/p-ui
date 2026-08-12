/* How full a kernel WireGuard inbound's address pool is, mirroring the Go
   allocator in internal/web/service/client_wireguard.go so the form can say it
   before the operator finds out from a routing table. */
export interface WireguardPoolUsage {
  /* The configured prefix clients are allocated from, as the device carries it. */
  prefix: string;
  capacity: number;
  used: number;
  /* Clients already sitting outside the prefix. Each one is a per-peer kernel
     route the reconciler diffs on every pass, forever. */
  outside: number;
}

export interface WireguardPoolClient {
  allowedIPs?: string[];
}

/* IPv4 only: the allocator widens past a full prefix for v4 alone, and a v6 pool
   is large enough that "how full is it" is never the question. */
function parseV4(value: string): number | null {
  const parts = value.trim().split('.');
  if (parts.length !== 4) return null;
  let out = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const n = Number(part);
    if (n > 255) return null;
    out = (out * 256) + n;
  }
  return out >>> 0;
}

function parsePrefix(value: string): { addr: number; bits: number } | null {
  const [head, tail] = value.trim().split('/');
  const addr = parseV4(head ?? '');
  if (addr === null) return null;
  const bits = tail === undefined ? 32 : Number(tail);
  if (!Number.isInteger(bits) || bits < 0 || bits > 32) return null;
  return { addr, bits };
}

/* Zero is special-cased because `x << 32` is a no-op in JS, and every result is
   forced unsigned because bit operations there are signed 32-bit. */
function networkOf(addr: number, bits: number): number {
  if (bits === 0) return 0;
  return (addr & (0xffffffff << (32 - bits))) >>> 0;
}

function contains(prefix: { addr: number; bits: number }, addr: number): boolean {
  return networkOf(prefix.addr, prefix.bits) === networkOf(addr, prefix.bits);
}

/* wireguardPoolUsage answers null when there is nothing to say: no IPv4 device
   address, or one the allocator would not treat as a pool. */
export function wireguardPoolUsage(
  addresses: string[] | undefined,
  clients: WireguardPoolClient[] | undefined,
): WireguardPoolUsage | null {
  const device = (addresses ?? []).map(parsePrefix).find((p): p is { addr: number; bits: number } => p !== null);
  if (!device) return null;

  const network = networkOf(device.addr, device.bits);
  /* The allocator's own candidate range: it starts at .2 and runs while the
     scope contains the address, so a /24 offers .2 through .255. */
  const capacity = Math.max(0, (2 ** (32 - device.bits)) - 2);

  const seen = new Set<number>();
  let outside = 0;
  for (const client of clients ?? []) {
    for (const raw of client.allowedIPs ?? []) {
      const held = parsePrefix(raw);
      /* A catch-all or a routed subnet is not an allocation and consumes no
         slot; the allocator keys `taken` on the address alone. */
      if (!held || held.bits !== 32) continue;
      if (contains(device, held.addr)) seen.add(held.addr);
      else outside += 1;
    }
  }

  return {
    prefix: `${[24, 16, 8, 0].map((s) => (network >>> s) & 255).join('.')}/${device.bits}`,
    capacity,
    used: seen.size,
    outside,
  };
}
