import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { CoreStatusViewSchema } from '@/generated/zod';
import type { CoreStatusView } from '@/generated/zod';

const CoreStatusViewsSchema = z.array(CoreStatusViewSchema);

/* The Cores page polls this every ten seconds — the supervise pass's cadence,
   so a restart it made shows within one refresh. */
export const CORES_STATUS_POLL_MS = 10_000;

async function fetchCoresStatus(): Promise<CoreStatusView[]> {
  const msg = await HttpUtil.get('/panel/api/cores/status', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch core status');
  const validated = parseMsg(msg, CoreStatusViewsSchema, 'cores/status');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useCoresStatusQuery() {
  const query = useQuery({
    queryKey: keys.cores.status(),
    queryFn: fetchCoresStatus,
    refetchInterval: CORES_STATUS_POLL_MS,
    refetchIntervalInBackground: false,
  });
  const cores = useMemo(() => query.data ?? [], [query.data]);
  return {
    cores,
    loading: query.isFetching,
    fetched: query.data !== undefined || query.isError,
    fetchError: query.error ? (query.error as Error).message : '',
    refetch: query.refetch,
  };
}
