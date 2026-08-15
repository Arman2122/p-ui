import { useMutation, useQueryClient } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import type { EgressPayload } from '@/schemas/api/egress';

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

/* Every write posts the whole row: the backend binds into a zero-value Egress,
   so a partial body would blank `type` and be refused as an unknown driver. */
export function useEgressMutations() {
  const queryClient = useQueryClient();
  /* Routing too, not just egresses: the rule editor's destination picker reads
     routing/exits, so without this a new uplink is missing from it until a reload. */
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: keys.egresses.root() });
    void queryClient.invalidateQueries({ queryKey: keys.routing.root() });
  };

  const addMut = useMutation({
    mutationFn: (payload: EgressPayload) => HttpUtil.post('/panel/api/egresses/add', payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: EgressPayload }) =>
      HttpUtil.post(`/panel/api/egresses/update/${id}`, payload, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const removeMut = useMutation({
    mutationFn: (id: number) => HttpUtil.post(`/panel/api/egresses/del/${id}`),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    add: (payload: EgressPayload) => addMut.mutateAsync(payload),
    update: (id: number, payload: EgressPayload) => updateMut.mutateAsync({ id, payload }),
    remove: (id: number) => removeMut.mutateAsync(id),
    saving: addMut.isPending || updateMut.isPending || removeMut.isPending,
  };
}
