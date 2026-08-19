import { isKernelWireguard } from './kernel-wireguard';

/*
Two kernel devices sharing a tunnel subnet is the failure this prevents.

Both install the same connected route into the one host routing table, so a
client of one inbound is answered over the other's device and encrypted to the
wrong peer. The server refuses it on save; catching it here means the operator
learns while typing rather than after filling in a whole form.

Deliberately the same question the server asks -- overlap, not equality: a /16
that contains another inbound's /24 collides just as completely.
*/

/* settings is unknown because a DBInbound carries it as raw JSON that may
   already be parsed; addressesOf handles both rather than forcing a cast at
   every call site. */
export interface SubnetOwner {
  id: number;
  remark?: string;
  protocol?: string;
  settings?: unknown;
}

interface Prefix {
  /* Big-endian bytes, so v4 and v6 compare the same way. */
  bytes: number[];
  bits: number;
}

function parsePrefix(value: string): Prefix | null {
  const [address, maskText] = value.trim().split('/');
  if (!address || !maskText) return null;
  const bits = Number(maskText);
  if (!Number.isInteger(bits) || bits < 0) return null;

  if (address.includes(':')) {
    // Only what is needed to compare: a full parse belongs to the server.
    const halves = address.split('::');
    if (halves.length > 2) return null;
    const head = halves[0] ? halves[0].split(':') : [];
    const tail = halves.length === 2 && halves[1] ? halves[1].split(':') : [];
    const missing = 8 - head.length - tail.length;
    if (missing < 0 || bits > 128) return null;
    const groups = [...head, ...Array(halves.length === 2 ? missing : 0).fill('0'), ...tail];
    if (groups.length !== 8) return null;
    const bytes: number[] = [];
    for (const group of groups) {
      const n = Number.parseInt(group || '0', 16);
      if (Number.isNaN(n)) return null;
      bytes.push((n >> 8) & 0xff, n & 0xff);
    }
    return { bytes, bits };
  }

  const parts = address.split('.');
  if (parts.length !== 4 || bits > 32) return null;
  const bytes = parts.map((p) => Number(p));
  if (bytes.some((b) => !Number.isInteger(b) || b < 0 || b > 255)) return null;
  return { bytes, bits };
}

/* Overlap is mutual containment at the shorter mask, which is exactly what
   netip's Overlaps does on the server. */
function overlaps(left: Prefix, right: Prefix): boolean {
  if (left.bytes.length !== right.bytes.length) return false;
  const shared = Math.min(left.bits, right.bits);
  for (let i = 0; i < left.bytes.length; i += 1) {
    const bitsHere = Math.min(8, Math.max(0, shared - i * 8));
    if (bitsHere === 0) return true;
    const mask = (0xff << (8 - bitsHere)) & 0xff;
    if ((left.bytes[i] & mask) !== (right.bytes[i] & mask)) return false;
  }
  return true;
}

function addressesOf(settings: unknown): string[] {
  let parsed: unknown = settings;
  if (typeof settings === 'string') {
    try {
      parsed = JSON.parse(settings);
    } catch {
      return [];
    }
  }
  const address = (parsed as { address?: unknown } | null)?.address;
  return Array.isArray(address) ? address.filter((a): a is string => typeof a === 'string') : [];
}

/* Every subnet already served by a kernel device, excluding the inbound being
   edited — its own address is not a conflict with itself. */
export function takenSubnets(inbounds: SubnetOwner[] | undefined, editingId?: number): { cidr: string; owner: SubnetOwner }[] {
  const out: { cidr: string; owner: SubnetOwner }[] = [];
  for (const inbound of inbounds ?? []) {
    if (!isKernelWireguard(inbound.protocol)) continue;
    if (editingId != null && inbound.id === editingId) continue;
    for (const cidr of addressesOf(inbound.settings)) {
      out.push({ cidr, owner: inbound });
    }
  }
  return out;
}

/* The first taken subnet this address collides with, or null. */
export function conflictingSubnet(
  address: string,
  taken: { cidr: string; owner: SubnetOwner }[],
): { cidr: string; owner: SubnetOwner } | null {
  const want = parsePrefix(address);
  if (!want) return null;
  for (const entry of taken) {
    const other = parsePrefix(entry.cidr);
    if (other && overlaps(want, other)) return entry;
  }
  return null;
}

/*
A free 10.x.0.1/24, so a second kernel inbound does not open pre-filled with a
subnet the server will refuse.

10.0.0.1/24 was the default for every inbound, which is fine for the first one
and guaranteed to collide for the next.
*/
export function suggestFreeSubnet(taken: { cidr: string; owner: SubnetOwner }[]): string {
  for (let octet = 0; octet < 256; octet += 1) {
    const candidate = `10.${octet}.0.1/24`;
    if (!conflictingSubnet(candidate, taken)) return candidate;
  }
  // 256 kernel inbounds on one host is not a case worth inventing a rule for;
  // the server still refuses a genuine collision.
  return '10.0.0.1/24';
}
