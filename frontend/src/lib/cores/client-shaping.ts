import type { CoreView } from '@/generated/zod';

/*
Whether a client's traffic can carry a rate limit, asked of what the cores
declare rather than of a protocol ladder.

Source of truth: internal/core/caps.go's Selector. A kind is listed only when
its core gives each client a kernel identity, so an absent kind is the honest
"this protocol cannot be rate limited", not an unknown.
*/

export interface ShapingVerdict {
  /* Kinds whose clients the kernel can tell apart, in the order asked. */
  shapeable: string[];
  /* Kinds that keep quota, expiry and IP limits but can carry no rate. */
  unshapeable: string[];
}

export function shapingForKinds(cores: CoreView[], kinds: string[]): ShapingVerdict {
  const declared = new Set<string>();
  for (const core of cores) {
    for (const [kind, selector] of Object.entries(core.shaping ?? {})) {
      if (selector) declared.add(kind);
    }
  }
  const shapeable: string[] = [];
  const unshapeable: string[] = [];
  for (const kind of new Set(kinds)) {
    (declared.has(kind) ? shapeable : unshapeable).push(kind);
  }
  return { shapeable, unshapeable };
}

/* Every kind any core in this build can shape, for a page that has no client
   in hand and must still name which protocols a plan's rates reach. */
export function shapeableKinds(cores: CoreView[]): string[] {
  const out = new Set<string>();
  for (const core of cores) {
    for (const [kind, selector] of Object.entries(core.shaping ?? {})) {
      if (selector) out.add(kind);
    }
  }
  return [...out].sort();
}
