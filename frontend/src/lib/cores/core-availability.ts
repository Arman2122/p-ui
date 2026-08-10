import type { CoreView } from '@/generated/zod';

/*
Which inbound kinds this host cannot serve, asked of each core's own Preflight
answer rather than of a hard-coded list.
*/

/* Kind -> the core's own reason, present only for kinds this host cannot run.
   No manifest blocks nothing: an older master, a failed fetch or a loading
   query must offer everything rather than lock the operator out. */
export function unavailableKinds(cores: CoreView[] | undefined): Map<string, string> {
  const blocked = new Map<string, string>();
  if (!cores?.length) return blocked;
  for (const core of cores) {
    if (core.available !== false) continue;
    for (const kind of core.kinds ?? []) {
      blocked.set(kind, core.unavailable || '');
    }
  }
  return blocked;
}
