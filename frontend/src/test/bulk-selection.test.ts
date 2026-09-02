import { describe, expect, it } from 'vitest';

import { filterSelection, isEmptySelection, selectionBody } from '@/lib/clients/bulk-selection';

/*
"Select all matching" sends the list filter, not the page's emails.

The Clients page could only act on the rows of the current server page. A
selection may now be the page's own query, stripped of paging and sort so
the server resolves every client the filter matches. Bulk operations, phase 1.
*/
describe('bulk selection payloads', () => {
  it('turns the paged query into a filter without paging or sort keys', () => {
    const selection = filterSelection({ page: 3, pageSize: 50, sort: 'email', order: 'asc', group: 'vip', search: '', protocol: undefined, inbound: '7' });
    expect(selectionBody(selection)).toEqual({ filter: { group: 'vip', inbound: '7' } });
    expect(isEmptySelection(selection)).toBe(false);
  });

  it('keeps an explicit email list as emails', () => {
    expect(selectionBody({ emails: ['a@x', 'b@x'] })).toEqual({ emails: ['a@x', 'b@x'] });
    expect(isEmptySelection({ emails: [] })).toBe(true);
  });
});
