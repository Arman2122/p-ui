import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { Status } from '@/models/status';
import { StatusSchema } from '@/schemas/status';
import { keys } from '@/api/queryKeys';
import { getSharedWebSocketClient } from '@/api/websocket';

const POLL_INTERVAL_MS = 2000;
/* While the socket is up the bridge writes a fresh snapshot into this cache */
/* every 2s, so the REST poll only has to cover a socket that is down. F345. */
const SOCKET_FALLBACK_POLL_MS = 15000;

async function fetchStatus(): Promise<Status> {
  const msg = await HttpUtil.get('/panel/api/server/status', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch status');
  const validated = parseMsg(msg, StatusSchema, 'server/status');
  return new Status(validated.obj);
}

export function useStatusQuery() {
  const query = useQuery({
    queryKey: keys.server.status(),
    queryFn: fetchStatus,
    refetchInterval: () => (getSharedWebSocketClient().isConnected ? SOCKET_FALLBACK_POLL_MS : POLL_INTERVAL_MS),
    refetchIntervalInBackground: false,
    staleTime: 0,
  });

  const status = useMemo(() => query.data ?? new Status(), [query.data]);
  const refresh = async () => { await query.refetch(); };

  return {
    status,
    fetched: query.data !== undefined || query.isError,
    fetchError: query.error ? (query.error as Error).message : '',
    refresh,
  };
}
