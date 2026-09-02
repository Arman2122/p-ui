import { describe, expect, it } from 'vitest';

import { EMPTY_BULK_SET, GB, buildBulkSetPatch, firstBulkSetIssue } from '@/lib/clients/bulk-set';

/*
The tri-state form must reach the wire as "absent = keep, zero = clear":
a row left on Keep never appears in the body, Clear sends the zero value,
Set sends the value with GB scaled to bytes. Bulk operations, phase 2.
*/
describe('buildBulkSetPatch', () => {
  it('omits kept rows, zeroes cleared rows and scales set rows', () => {
    const patch = buildBulkSetPatch({
      ...EMPTY_BULK_SET,
      limitIp: { mode: 'set', value: 3 },
      comment: { mode: 'set', value: '  Q4 batch ' },
      totalGB: { mode: 'set', value: 1.5 },
      expiryTime: { mode: 'clear', value: 1767225600000 },
      flow: { mode: 'clear', value: 'xtls-rprx-vision' },
      policyId: { mode: 'set', value: 7 },
    });
    expect(patch).toEqual({
      limitIp: 3,
      comment: 'Q4 batch',
      totalGB: Math.trunc(1.5 * GB),
      expiryTime: 0,
      flow: '',
      policyId: 7,
    });
    expect('tgId' in patch).toBe(false);
    expect('group' in patch).toBe(false);
  });

  it('names an empty form and a Set row without a value', () => {
    expect(firstBulkSetIssue(EMPTY_BULK_SET)).toBe('pages.clients.bulkSetNothing');
    expect(firstBulkSetIssue({ ...EMPTY_BULK_SET, expiryTime: { mode: 'set', value: null } })).toBe('pages.clients.bulkSetMissingValue');
    expect(firstBulkSetIssue({ ...EMPTY_BULK_SET, limitIp: { mode: 'clear', value: 0 } })).toBeNull();
  });
});
