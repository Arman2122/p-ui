import { describe, expect, it } from 'vitest';

import { EGRESS_TYPE, EGRESS_TYPE_AWG_UPLINK, EGRESS_TYPE_SSH, EGRESS_TYPE_UPLINK, type EgressRecord } from '@/schemas/api/egress';
import { exitAsOutboundRow, isUntestable } from '@/pages/xray/outbounds/outbounds-tab-helpers';

/* The backend times every enabled exit that is not a front — the SSH exit
   through its SOCKS port, both uplinks through their mark — yet the page only
   let the WireGuard uplink be tested, so an SSH exit's test buttons were dead
   while the API behind them worked. */
describe('which exits the outbounds page lets you test', () => {
  const row = (type: string, enable = true): EgressRecord =>
    ({ id: 3, type, remark: `${type} exit`, enable, target: '', settings: '' }) as EgressRecord;

  it('offers every enabled exit that is not a front', () => {
    for (const type of [EGRESS_TYPE_SSH, EGRESS_TYPE_UPLINK, EGRESS_TYPE_AWG_UPLINK]) {
      expect(isUntestable(exitAsOutboundRow(row(type))), type).toBe(false);
    }
  });

  it('refuses a front and a disabled exit, as the backend does', () => {
    expect(isUntestable(exitAsOutboundRow(row(EGRESS_TYPE)))).toBe(true);
    expect(isUntestable(exitAsOutboundRow(row(EGRESS_TYPE_SSH, false)))).toBe(true);
  });
});
