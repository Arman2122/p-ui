import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';
import type { IpLimitStatus } from '@/generated/zod';

/* Fail open: until the host has answered, the field is offered rather than
   refused, and a refusal on a fetch error would hide a cap the host can hold. */
const FAIL_OPEN_STATUS: IpLimitStatus = { enabled: true, enforceable: true, reason: '' };

async function fetchIpLimitStatus(): Promise<IpLimitStatus> {
  const msg = await HttpUtil.get<IpLimitStatus>('/panel/api/server/ipLimitStatus', undefined, { silent: true });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch the IP limit status');
  return { ...FAIL_OPEN_STATUS, ...msg.obj };
}

type Translate = (key: string, options?: Record<string, string>) => string;

/* Why a cap the firewall would enforce cannot be, on this host: banning is
   turned off, or the panel cannot write its own ban table and says why. */
export function getLimitIpNotice(status: IpLimitStatus, t: Translate): string {
  if (status.enabled && status.enforceable) return '';
  if (!status.enabled) return t('pages.clients.limitIpDisabled');
  return t('pages.clients.limitIpNotEnforceable', { reason: status.reason });
}

export function useIpLimitStatusQuery() {
  const query = useQuery({
    queryKey: keys.server.ipLimitStatus(),
    queryFn: fetchIpLimitStatus,
    staleTime: 60_000,
  });

  return query.data ?? FAIL_OPEN_STATUS;
}
