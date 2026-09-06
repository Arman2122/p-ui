import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { BanViewSchema } from '@/generated/zod';
import type { BanView } from '@/generated/zod';

const BanViewsSchema = z.array(BanViewSchema);
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

/* The pass that writes bans runs every ten seconds; a list refreshed at that
   cadence shows a ban within one refresh of the pass that made it. */
export const BANS_POLL_MS = 10_000;

async function fetchBans(): Promise<BanView[]> {
  const msg = await HttpUtil.get('/panel/api/policies/bans', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch bans');
  const validated = parseMsg(msg, BanViewsSchema, 'policies/bans');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useBansQuery(enabled = true) {
  const query = useQuery({
    queryKey: keys.bans.list(),
    queryFn: fetchBans,
    enabled,
    refetchInterval: BANS_POLL_MS,
    refetchIntervalInBackground: false,
  });
  const bans = useMemo(() => query.data ?? [], [query.data]);
  return { bans, loading: query.isFetching, refetch: query.refetch };
}

export function useBanMutations() {
  const queryClient = useQueryClient();
  const invalidate = () => { void queryClient.invalidateQueries({ queryKey: keys.bans.root() }); };

  const releaseMut = useMutation({
    mutationFn: ({ email, source }: { email: string; source: string }) =>
      HttpUtil.post('/panel/api/policies/bans/release', { email, source }, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });
  const releaseAllMut = useMutation({
    mutationFn: () => HttpUtil.post('/panel/api/policies/bans/releaseAll'),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });
  const addMut = useMutation({
    mutationFn: ({ source, minutes }: { source: string; minutes: number }) =>
      HttpUtil.post('/panel/api/policies/bans/add', { source, minutes }, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  return {
    release: (email: string, source: string) => releaseMut.mutateAsync({ email, source }),
    releaseAll: () => releaseAllMut.mutateAsync(),
    ban: (source: string, minutes: number) => addMut.mutateAsync({ source, minutes }),
    saving: releaseMut.isPending || releaseAllMut.isPending || addMut.isPending,
  };
}
