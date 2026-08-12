/* Validation for a kernel WireGuard interface address, at the point it is typed.

   The server parses each entry with netip.ParsePrefix and SILENTLY DROPS what
   fails, so a typo does not error — the address simply never applies and the
   device answers on nothing. Catching it here is what turns that into a
   sentence the operator can act on. */

/* The prefix a bare address is completed to. /24 is the panel's own
   defaultWireguardBase, and /64 is the smallest v6 subnet SLAAC allows. */
const DEFAULT_V4_BITS = 24;
const DEFAULT_V6_BITS = 64;

export type InterfaceAddressResult =
  | { ok: true; value: string }
  /* reason is a translation key, because this sentence is shown to the operator
     and the panel ships two locales. */
  | { ok: false; reason: string };

function isV4(value: string): boolean {
  const parts = value.split('.');
  if (parts.length !== 4) return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

/* Deliberately not a full RFC 4291 parser: it accepts the shapes a person types
   and rejects what has no chance of being an address, leaving the exact ruling
   to the server's own netip parse. */
function isV6(value: string): boolean {
  if (!value.includes(':')) return false;
  if (!/^[0-9a-fA-F:.]+$/.test(value)) return false;
  if ((value.match(/::/g) ?? []).length > 1) return false;
  const groups = value.split(':').filter((g) => g !== '');
  return groups.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g) || isV4(g));
}

/*
normalizeInterfaceAddress accepts what an operator means and refuses the rest.

A bare address is completed rather than rejected: on this field it can only mean
"this address, with an ordinary subnet", and making someone retype it with a
prefix teaches them nothing. Anything that is not an address at all is refused
with the reason, because the alternative is the server dropping it in silence.
*/
export function normalizeInterfaceAddress(raw: string): InterfaceAddressResult {
  const value = raw.trim();
  if (value === '') return { ok: false, reason: 'pages.inbounds.form.wgkernelAddressInvalid' };

  const slash = value.indexOf('/');
  const host = slash === -1 ? value : value.slice(0, slash);
  const tail = slash === -1 ? '' : value.slice(slash + 1);

  const v4 = isV4(host);
  const v6 = !v4 && isV6(host);
  if (!v4 && !v6) return { ok: false, reason: 'pages.inbounds.form.wgkernelAddressInvalid' };

  if (slash === -1) {
    return { ok: true, value: `${host}/${v4 ? DEFAULT_V4_BITS : DEFAULT_V6_BITS}` };
  }
  if (!/^\d{1,3}$/.test(tail)) {
    return { ok: false, reason: 'pages.inbounds.form.wgkernelAddressInvalid' };
  }
  const bits = Number(tail);
  const max = v4 ? 32 : 128;
  if (bits > max) return { ok: false, reason: 'pages.inbounds.form.wgkernelAddressPrefixRange' };

  /* A device address is where the server itself answers, so it carries a host
     part: 10.0.0.0/24 routes the range but is nobody's address. */
  if (v4 && bits === 32) return { ok: false, reason: 'pages.inbounds.form.wgkernelAddressNoRoom' };

  return { ok: true, value: `${host}/${bits}` };
}

/* normalizeInterfaceAddresses filters a whole list, keeping the good entries in
   order and returning the first reason so a caller can say what was dropped. */
export function normalizeInterfaceAddresses(values: string[]): {
  values: string[];
  rejected: string[];
  reason: string | null;
} {
  const out: string[] = [];
  const rejected: string[] = [];
  let reason: string | null = null;
  for (const raw of values) {
    const result = normalizeInterfaceAddress(raw);
    if (result.ok) {
      if (!out.includes(result.value)) out.push(result.value);
    } else {
      rejected.push(raw.trim());
      reason = reason ?? result.reason;
    }
  }
  return { values: out, rejected, reason };
}
