import type { CoreView } from '@/generated/zod';

/*
Which protocols an outbound picker may offer, asked of the core registry rather
than of one list per core maintained by hand.
*/

export interface OutboundProtocolOption {
  kind: string;
  coreId: string;
  /* Why this host cannot run the core behind it; '' when it can. The picker
     explains a blocked choice rather than omitting it, as inbounds already do. */
  unavailable: string;
}

export interface OutboundProtocolGroup {
  coreId: string;
  titleKey: string;
  options: OutboundProtocolOption[];
}

/* Every kind a route may terminate on, flattened across cores. */
export function registryExitKinds(cores: CoreView[] | undefined): OutboundProtocolOption[] {
  const out: OutboundProtocolOption[] = [];
  for (const core of cores ?? []) {
    for (const kind of core.exitKinds ?? []) {
      out.push({
        kind,
        coreId: core.id,
        unavailable: core.available === false ? core.unavailable || '' : '',
      });
    }
  }
  return out;
}

/*
The picker's grouped options: the built-in protocols first, under the core that
serves them, then every other core's exit kinds under their own heading.

Grouped rather than flat because two cores can offer near-identical names —
Xray's userspace `wireguard` outbound is not the `wgkernel` core — and the
heading is the only thing that tells an operator which one they are choosing.
*/
export function outboundProtocolGroups(
  cores: CoreView[] | undefined,
  builtin: { coreId: string; kinds: readonly string[] },
): OutboundProtocolGroup[] {
  const byId = new Map((cores ?? []).map((core) => [core.id, core]));
  const seen = new Set<string>();
  const groups: OutboundProtocolGroup[] = [];

  const host = byId.get(builtin.coreId);
  const builtinOptions = builtin.kinds.map((kind) => {
    seen.add(kind);
    return {
      kind,
      coreId: builtin.coreId,
      unavailable: host?.available === false ? host.unavailable || '' : '',
    };
  });
  if (builtinOptions.length) {
    groups.push({
      coreId: builtin.coreId,
      titleKey: host?.titleKey || `cores.${builtin.coreId}.title`,
      options: builtinOptions,
    });
  }

  for (const core of cores ?? []) {
    if (core.id === builtin.coreId) continue;
    const options = registryExitKinds([core]).filter((option) => !seen.has(option.kind));
    for (const option of options) seen.add(option.kind);
    if (options.length) groups.push({ coreId: core.id, titleKey: core.titleKey, options });
  }
  return groups;
}
