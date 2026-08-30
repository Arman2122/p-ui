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
