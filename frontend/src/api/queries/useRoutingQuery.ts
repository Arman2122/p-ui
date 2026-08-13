import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import {
  RoutingRuleListSchema,
  RoutingSubjectViewListSchema,
  type RoutingRuleRecord,
  type RoutingSubjectView,
} from '@/schemas/api/routing';

async function fetchRules(): Promise<RoutingRuleRecord[]> {
  const msg = await HttpUtil.get('/panel/api/routing/rules', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch routing rules');
  const validated = parseMsg(msg, RoutingRuleListSchema, 'routing/rules');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useRoutingRulesQuery(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.routing.rules(),
    queryFn: fetchRules,
    enabled: options.enabled ?? true,
  });
}

async function fetchSubjects(): Promise<RoutingSubjectView[]> {
  const msg = await HttpUtil.get('/panel/api/routing/subjects', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch routing subjects');
  const validated = parseMsg(msg, RoutingSubjectViewListSchema, 'routing/subjects');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

/* Which inbounds a rule may name, and why not when it may not. Refetched with
   the rules because turning an inbound's bridge on changes the answer. */
export function useRoutingSubjectsQuery(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.routing.subjects(),
    queryFn: fetchSubjects,
    enabled: options.enabled ?? true,
  });
}
