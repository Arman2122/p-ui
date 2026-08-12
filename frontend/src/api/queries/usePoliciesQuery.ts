import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import {
  EnforcedLimitsSchema,
  PolicyListSchema,
  type EnforcedRecord,
  type PolicyRecord,
} from '@/schemas/api/policy';

async function fetchPolicies(): Promise<PolicyRecord[]> {
  const msg = await HttpUtil.get('/panel/api/policies/list', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch plans');
  const validated = parseMsg(msg, PolicyListSchema, 'policies/list');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function usePoliciesQuery(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: keys.policies.list(),
    queryFn: fetchPolicies,
    enabled: options.enabled ?? true,
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
