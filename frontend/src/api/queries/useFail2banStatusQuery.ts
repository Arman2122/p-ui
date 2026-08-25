import { useQuery } from '@tanstack/react-query';

import { HttpUtil } from '@/utils';
import { keys } from '@/api/queryKeys';

export interface Fail2banStatus {
  enabled: boolean;
  installed: boolean;
  jailActive: boolean;
  usable: boolean;
}

const FAIL_OPEN_STATUS: Fail2banStatus = {
  enabled: true,
  installed: true,
  jailActive: true,
  usable: true,
};

async function fetchFail2banStatus(): Promise<Fail2banStatus> {
  const msg = await HttpUtil.get<Fail2banStatus>('/panel/api/server/fail2banStatus', undefined, { silent: true });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch fail2ban status');
  return { ...FAIL_OPEN_STATUS, ...msg.obj };
}

export function getLimitIpNotice(status: Fail2banStatus, t: (key: string) => string): string {
  if (status.usable) return '';
  if (!status.enabled) return t('pages.clients.limitIpDisabled');
  if (!status.installed) return t('pages.clients.limitIpFail2banMissing');
  // Installed but the jail is gone: the binary answers -h either way, so this
  // is the state that used to read as enforcing while dropping nothing.
  return t('pages.clients.limitIpJailMissing');
}

export function useFail2banStatusQuery() {
  const query = useQuery({
    queryKey: keys.server.fail2banStatus(),
    queryFn: fetchFail2banStatus,
    staleTime: 60_000,
  });

  return query.data ?? FAIL_OPEN_STATUS;
}
