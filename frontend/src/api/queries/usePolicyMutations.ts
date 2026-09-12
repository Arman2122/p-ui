import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import { PolicyPayloadSchema, type PolicyPayload } from '@/schemas/api/policy';
import { resellerKeys } from '@/pages/resellers/api';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

/* Assignment is its own call rather than a field on the client payload: a
   caller written before plans existed would otherwise unassign every client. */
export function usePolicyMutations(reseller = false) {
  const base = reseller ? '/panel/api/reseller/plans' : '/panel/api/policies';
  const queryClient = useQueryClient();
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: keys.policies.root() });
    void queryClient.invalidateQueries({ queryKey: keys.clients.root() });
    if (reseller) void queryClient.invalidateQueries({ queryKey: resellerKeys });
  };

  const addMut = useMutation({
    mutationFn: (payload: PolicyPayload) => HttpUtil.post(`${base}/add`, PolicyPayloadSchema.parse(payload), JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: PolicyPayload }) =>
      HttpUtil.post(`${base}/update/${id}`, PolicyPayloadSchema.parse(payload), JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`${base}/del/${id}`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const assignMut = useMutation({
    mutationFn: ({ email, policyId }: { email: string; policyId: number }) =>
      HttpUtil.post('/panel/api/policies/assign', { email, policyId }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const actionMut = useMutation({
    mutationFn: ({ action, id, payload }: { action: 'archive' | 'duplicate' | 'reapply'; id: number; payload: object }) =>
      HttpUtil.post(`${base}/${action}/${id}`, payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    add: (payload: PolicyPayload) => addMut.mutateAsync(payload),
    update: (id: number, payload: PolicyPayload) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    assign: (email: string, policyId: number) => assignMut.mutateAsync({ email, policyId }),
    archive: (id: number, archived: boolean) => actionMut.mutateAsync({ action: 'archive', id, payload: { archived } }),
    duplicate: (id: number, name: string) => actionMut.mutateAsync({ action: 'duplicate', id, payload: { name } }),
    reapply: (id: number, clearOverrides: boolean) => actionMut.mutateAsync({ action: 'reapply', id, payload: { clearOverrides } }),
    saving: addMut.isPending || updateMut.isPending || removeMut.isPending || actionMut.isPending,
  };
}
