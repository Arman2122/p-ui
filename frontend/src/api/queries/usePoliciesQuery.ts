import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { InboundOptionsSchema } from '@/schemas/client';
import {
  EnforcedLimitsSchema,
  PolicyListSchema,
  PlanMemberPageSchema,
  PlanSummarySchema,
  type EnforcedRecord,
  type PolicyRecord,
} from '@/schemas/api/policy';

async function fetchPolicies(base: string): Promise<PolicyRecord[]> {
  const msg = await HttpUtil.get(`${base}/list`, undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch plans');
  const validated = parseMsg(msg, PolicyListSchema, 'policies/list');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function usePoliciesQuery(options: { enabled?: boolean; reseller?: boolean } = {}) {
  const base = options.reseller ? '/panel/api/reseller/plans' : '/panel/api/policies';
  return useQuery({
    queryKey: options.reseller ? [...keys.policies.list(), 'reseller'] : keys.policies.list(),
    queryFn: () => fetchPolicies(base),
    enabled: options.enabled ?? true,
  });
}

export function usePlanMembersQuery(id: number | undefined, page: number, reseller = false) {
  const base = reseller ? '/panel/api/reseller/plans' : '/panel/api/policies';
  return useQuery({
    queryKey: [...keys.policies.root(), 'members', id, page, reseller],
    enabled: !!id,
    queryFn: async () => {
      const msg = await HttpUtil.get(`${base}/members/${id}`, { page, pageSize: 20 }, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Failed to load plan members');
      const parsed = parseMsg(msg, PlanMemberPageSchema, 'plan/members');
      if (!parsed.obj) throw new Error('Invalid plan members');
      return parsed.obj;
    },
  });
}

export function usePlanInboundOptions(reseller = false) {
  return useQuery({
    queryKey: reseller ? [...keys.policies.root(), 'inbounds', 'reseller'] : keys.inbounds.options(),
    queryFn: async () => {
      const msg = await HttpUtil.get(reseller ? '/panel/api/reseller/inbounds' : '/panel/api/inbounds/options', undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Failed to load inbounds');
      return parseMsg(msg, InboundOptionsSchema, 'plans/inbounds').obj ?? [];
    },
  });
}

export function usePlanSummaryQuery(reseller = false) {
  return useQuery({
    queryKey: [...keys.policies.root(), 'summary', reseller],
    queryFn: async () => {
      const base = reseller ? '/panel/api/reseller/plans' : '/panel/api/policies';
      const msg = await HttpUtil.get(`${base}/summary`, undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Failed to load plan summary');
      const parsed = parseMsg(msg, PlanSummarySchema, 'plans/summary');
      if (!parsed.obj) throw new Error('Invalid plan summary');
      return parsed.obj;
    },
  });
}

async function fetchEnforced(email: string): Promise<EnforcedRecord> {
  const msg = await HttpUtil.get(`/panel/api/policies/enforced/${encodeURIComponent(email)}`, undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to read the enforced limits');
  const validated = parseMsg(msg, EnforcedLimitsSchema, 'policies/enforced');
  if (!validated.obj) throw new Error('Malformed enforced limits');
  return validated.obj;
}

/* Kernel state, not row state: the enforced half is read back off the device
   every time, because a limit that never landed is invisible otherwise. */
export function useEnforcedLimitsQuery(email: string, options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.policies.enforced(email),
    queryFn: () => fetchEnforced(email),
    enabled: (options.enabled ?? true) && email !== '',
    retry: false,
  });
}
