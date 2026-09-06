import type { CoreView } from '@/generated/zod';

/*
Which of a client's inbounds an IP limit can actually be enforced on.

The cap is decided from live sessions: EvaluateIPLimits only ever sees clients a
core reported a session for, with the address it came from. A core that cannot
report sessions — mtg names a secret in use and no address at all — has no
client in that set, so a cap on one of its clients is a number that does
nothing, and the form used to accept it without a word.

Asked of what each core declares (`sessionKinds`), never of a protocol list
here: a core added later declares for itself, and a list in the frontend is the
thing that goes stale. F120 / F302.
*/
export function kindsWithoutIpLimit(cores: CoreView[] | undefined, kinds: string[]): string[] {
  if (!cores || kinds.length === 0) return [];
  const enforceable = new Set<string>();
  for (const core of cores) {
    for (const kind of core.sessionKinds ?? []) enforceable.add(kind);
  }
  /* A kind NO core claims is left alone: it is a quarantined or unknown inbound,
     and refusing its cap would be this file guessing on a core's behalf. */
  const claimed = new Set<string>();
  for (const core of cores) {
    for (const kind of core.kinds ?? []) claimed.add(kind);
  }
  const out: string[] = [];
  for (const kind of kinds) {
    if (claimed.has(kind) && !enforceable.has(kind) && !out.includes(kind)) out.push(kind);
  }
  return out;
}

/*
How a device over its cap is dealt with, per kind, as each core declares it:
"login" is refused at the core's own login and cut, and needs no firewall;
"cut" is cut by the core and dropped on the host firewall; "firewall" is the
host firewall alone, on the inbound's port.
*/
export type IpLimitEnforcement = 'login' | 'cut' | 'firewall';

export function ipLimitEnforcementOf(
  cores: CoreView[] | undefined,
  kinds: string[],
): Record<IpLimitEnforcement, string[]> {
  const out: Record<IpLimitEnforcement, string[]> = { login: [], cut: [], firewall: [] };
  if (!cores) return out;
  const byKind = new Map<string, IpLimitEnforcement>();
  for (const core of cores) {
    for (const [kind, mode] of Object.entries(core.ipLimitEnforcement ?? {})) {
      if (mode === 'login' || mode === 'cut' || mode === 'firewall') byKind.set(kind, mode);
    }
  }
  for (const kind of kinds) {
    const mode = byKind.get(kind);
    if (mode && !out[mode].includes(kind)) out[mode].push(kind);
  }
  return out;
}

export interface IpLimitFieldState {
  disabled: boolean;
  notice: string;
}

type Translate = (key: string, options?: Record<string, string>) => string;

/*
Whether the cap can be set for this client, and what to tell the operator.

Three reasons a cap may not bite, and they are not the same thing: the client
sits on an inbound whose core cannot report sessions; this host cannot write
its firewall and every attached kind relies on it; or only some of the attached
kinds do. Only the first two disable the field — a kind enforced at its own
login holds a cap on a host with no firewall at all, so the field stays open
and the notice says which kinds the firewall would have to cover.
*/
export function ipLimitFieldState(
  cores: CoreView[] | undefined,
  kinds: string[],
  firewallUsable: boolean,
  hostNotice: string,
  t: Translate,
): IpLimitFieldState {
  const unsupported = kindsWithoutIpLimit(cores, kinds);
  if (kinds.length > 0 && unsupported.length === kinds.length) {
    return { disabled: true, notice: t('pages.clients.limitIpUnsupportedKinds', { kinds: unsupported.join(', ') }) };
  }
  const { login, cut, firewall } = ipLimitEnforcementOf(cores, kinds);
  const needsFirewall = [...cut, ...firewall];
  if (login.length === 0 && !firewallUsable) {
    return { disabled: true, notice: hostNotice };
  }
  const parts: string[] = [];
  if (unsupported.length > 0) {
    parts.push(t('pages.clients.limitIpUnsupportedKinds', { kinds: unsupported.join(', ') }));
  }
  if (login.length > 0) {
    parts.push(t('pages.clients.limitIpEnforcedAtLogin', { kinds: login.join(', ') }));
  }
  if (needsFirewall.length > 0 && !firewallUsable) {
    parts.push(`${t('pages.clients.limitIpEnforcedByFirewall', { kinds: needsFirewall.join(', ') })} ${hostNotice}`.trim());
  }
  return { disabled: false, notice: parts.join(' ') };
}
