import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { keys } from '@/api/queryKeys';
import { HttpUtil } from '@/utils';
import { SlimInboundListSchema } from '@/schemas/inbound';
import type { CoreStatusView, CoreVersionsView, CoreView } from '@/generated/zod';
import { isBuiltinCore } from '@/lib/cores/display';

const SettingsSchema = z.object({ clients: z.array(z.object({ email: z.string() })).optional() });
const CoreInboundSchema = z.object({
  id: z.number(), protocol: z.string(), nodeId: z.number().nullish(),
  remark: z.string().optional(), tag: z.string().optional(), port: z.number().optional(),
  up: z.number().nonnegative().optional(), down: z.number().nonnegative().optional(),
  settings: z.unknown().optional(),
});
export type CoreInbound = z.infer<typeof CoreInboundSchema>;

export function coreMetrics(descriptor: CoreView | undefined, inbounds: CoreInbound[] | undefined) {
  if (!descriptor || !inbounds) return { inbounds: undefined, clients: undefined, up: undefined, down: undefined, traffic: undefined };
  const rows = inbounds.filter((row) => row.nodeId == null && descriptor.kinds.includes(row.protocol));
  const clients = new Set<string>();
  let clientsKnown = true;
  for (const row of rows) {
    let settings = row.settings;
    if (typeof settings === 'string') {
      try { settings = JSON.parse(settings); } catch { settings = undefined; }
    }
    const parsed = SettingsSchema.safeParse(settings);
    if (!parsed.success || !parsed.data.clients) clientsKnown = false;
    else parsed.data.clients.forEach((client) => clients.add(client.email));
  }
  const countersKnown = rows.every((row) => row.up !== undefined && row.down !== undefined);
  return {
    inbounds: rows,
    clients: clientsKnown ? clients.size : undefined,
    up: countersKnown ? rows.reduce((sum, row) => sum + row.up!, 0) : undefined,
    down: countersKnown ? rows.reduce((sum, row) => sum + row.down!, 0) : undefined,
    traffic: countersKnown
      ? rows.reduce((sum, row) => sum + row.up! + row.down!, 0) : undefined,
  };
}
export type CoreMetrics = ReturnType<typeof coreMetrics>;

export function useCoreInventory() {
  const descriptors = useCoresQuery();
  const query = useQuery({
    queryKey: keys.inbounds.slim(),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/inbounds/list/slim', undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Unable to read inbounds');
      return SlimInboundListSchema.parse(msg.obj);
    },
    refetchInterval: 10_000,
  });
  const parsed = useMemo(() => z.array(CoreInboundSchema).safeParse(query.data), [query.data]);
  const inbounds = parsed.success && !query.isError ? parsed.data : undefined;
  return { descriptors: descriptors.isError ? undefined : descriptors.data, inbounds,
    observedAt: query.isError ? 0 : query.dataUpdatedAt,
    error: descriptors.error?.message || query.error?.message, refetch: query.refetch };
}

export function coreUptime(core: CoreStatusView, now = Date.now()): number | undefined {
  if (!core.available || core.state !== 'running' || !core.ready) return undefined;
  const starts = core.instances.filter((row) => row.state === 'running' && row.ready && row.since).map((row) => row.since!);
  const since = core.since || (starts.length ? Math.min(...starts) : undefined);
  return since ? Math.max(0, Math.floor((now - since) / 1000)) : undefined;
}

export function newerCoreVersion(core: CoreStatusView, view: CoreVersionsView | undefined): string | null | undefined {
  if (!core.version.manageable) return null;
  if (!view || view.error || !view.manageable) return undefined;
  const parts = (version: string) => /^v?\d+\.\d+\.\d+$/.test(version) ? version.replace(/^v/, '').split('.').map(Number) : undefined;
  const installed = parts(view.installed || core.version.installed || '');
  if (!installed) return undefined;
  let newest: string | null = null;
  let current = installed;
  for (const version of view.available) {
    const candidate = parts(version);
    if (!candidate) continue;
    const diff = candidate.map((n, i) => n - current[i]).find((n) => n !== 0) ?? 0;
    if (diff > 0) { current = candidate; newest = version; }
  }
  return newest;
}

export function coreInstalled(core: CoreStatusView): boolean {
  return core.available || isBuiltinCore(core) || !!core.version.installed;
}
