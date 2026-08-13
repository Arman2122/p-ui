import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import type { RoutingRulePayload } from '@/schemas/api/routing';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

/* Every write converges before the response returns, so invalidating on success
   is enough — there is no window where the list disagrees with the kernel. */
export function useRoutingMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: keys.routing.root() });
  };

  const addMut = useMutation({
    mutationFn: (payload: RoutingRulePayload) =>
      HttpUtil.post('/panel/api/routing/rules', payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: RoutingRulePayload }) =>
      HttpUtil.post(`/panel/api/routing/rules/${id}`, payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/routing/rules/${id}/del`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const reorderMut = useMutation({
    mutationFn: (ids: number[]) =>
      HttpUtil.post('/panel/api/routing/rules/order', { ids }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    add: (payload: RoutingRulePayload) => addMut.mutateAsync(payload),
    update: (id: number, payload: RoutingRulePayload) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    reorder: (ids: number[]) => reorderMut.mutateAsync(ids),
    saving: addMut.isPending || updateMut.isPending || removeMut.isPending || reorderMut.isPending,
  };
}
