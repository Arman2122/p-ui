import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import type { PolicyPayload } from '@/schemas/api/policy';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

/* Assignment is its own call rather than a field on the client payload: a
   caller written before plans existed would otherwise unassign every client. */
export function usePolicyMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: keys.policies.root() });
  };

  const addMut = useMutation({
    mutationFn: (payload: PolicyPayload) => HttpUtil.post('/panel/api/policies/add', payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: PolicyPayload }) =>
      HttpUtil.post(`/panel/api/policies/update/${id}`, payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/policies/del/${id}`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const assignMut = useMutation({
    mutationFn: ({ email, policyId }: { email: string; policyId: number }) =>
      HttpUtil.post('/panel/api/policies/assign', { email, policyId }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    add: (payload: PolicyPayload) => addMut.mutateAsync(payload),
    update: (id: number, payload: PolicyPayload) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    assign: (email: string, policyId: number) => assignMut.mutateAsync({ email, policyId }),
    saving: addMut.isPending || updateMut.isPending || removeMut.isPending,
  };
}
