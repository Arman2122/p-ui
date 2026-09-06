import { describe, expect, it } from 'vitest';

import { getLimitIpNotice } from '@/api/queries/useIpLimitStatusQuery';
import type { IpLimitStatus } from '@/generated/zod';

/*
Two reasons a cap the firewall would enforce cannot be, and they are not the
same: banning is turned off on this host, or the panel cannot write its own
ban table and says why. The notice must carry the host's reason, since the
operator's fix differs for a missing nft and for a panel not running as root.
*/
const t = (key: string, options?: Record<string, string>) => `${key.split('.').pop()}(${options?.reason ?? ''})`;

const cases: { name: string; status: IpLimitStatus; want: string }[] = [
  { name: 'the host can drop addresses', status: { enabled: true, enforceable: true, reason: '' }, want: '' },
  { name: 'banning is turned off here', status: { enabled: false, enforceable: true, reason: '' }, want: 'limitIpDisabled()' },
  {
    name: 'the panel cannot write its ban table',
    status: { enabled: true, enforceable: false, reason: 'banset: nft is not installed' },
    want: 'limitIpNotEnforceable(banset: nft is not installed)',
  },
];

describe('why a cap the firewall would enforce cannot be', () => {
  for (const c of cases) {
    it(c.name, () => {
      expect(getLimitIpNotice(c.status, t)).toBe(c.want);
    });
  }
});
