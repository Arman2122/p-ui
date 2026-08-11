import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import {
  EgressListSchema,
  EgressPreflightSchema,
  type EgressPreflight,
  type EgressRecord,
} from '@/schemas/api/egress';

async function fetchEgresses(): Promise<EgressRecord[]> {
  const msg = await HttpUtil.get('/panel/api/egresses/list', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch egresses');
  const validated = parseMsg(msg, EgressListSchema, 'egresses/list');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useEgressesQuery(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.egresses.list(),
    queryFn: fetchEgresses,
    enabled: options.enabled ?? true,
  });
}

async function fetchEgressPreflight(): Promise<EgressPreflight> {
  const msg = await HttpUtil.get('/panel/api/egresses/preflight', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to run the egress preflight');
  const validated = parseMsg(msg, EgressPreflightSchema, 'egresses/preflight');
  const report = validated.obj;
  if (!report || typeof report.ok !== 'boolean') throw new Error('Malformed egress preflight report');
  return { ok: report.ok, refusals: report.refusals ?? [], notes: report.notes ?? [] };
}

/* Host state, not row state: it changes when someone edits a sysctl on the box,
   so it is refetched with the list rather than cached for the session. */
export function useEgressPreflightQuery() {
  return useQuery({
    queryKey: keys.egresses.preflight(),
    queryFn: fetchEgressPreflight,
  });
}
