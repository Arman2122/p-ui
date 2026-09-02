import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';

import BulkResultDrawer from '@/pages/clients/BulkResultDrawer';
import { renderWithProviders } from './test-utils';

/*
A bulk run that left rows behind shows every one of them with its reason.

The toasts carried a count and the first skipped reason; the rest were lost.
The drawer lists skipped and failed rows and hands the failed emails back for
a retry. Bulk operations, phase 1.
*/
describe('BulkResultDrawer', () => {
  it('lists the rows with reasons and retries exactly the failed emails', () => {
    const onRetry = vi.fn();
    renderWithProviders(
      <BulkResultDrawer
        open
        title="Delete: 3 clients"
        outcome={{ ok: ['a@x'], skipped: [{ email: 'b@x', reason: 'unlimited expiry' }], failed: [{ email: 'c@x', reason: 'inbound 9: down' }] }}
        onClose={() => {}}
        onRetry={onRetry}
      />,
    );
    expect(screen.getByTestId('bulk-ok-count').textContent).toContain('1');
    expect(screen.getByTestId('bulk-skipped-count').textContent).toContain('1');
    expect(screen.getByTestId('bulk-failed-count').textContent).toContain('1');
    expect(screen.getByText('unlimited expiry')).toBeTruthy();
    expect(screen.getByText('inbound 9: down')).toBeTruthy();

    fireEvent.click(screen.getByText(/retry/i));
    expect(onRetry).toHaveBeenCalledWith(['c@x']);
  });

  it('says so when nothing was left behind', () => {
    renderWithProviders(
      <BulkResultDrawer open title="Enable: 2 clients" outcome={{ ok: ['a@x', 'b@x'], skipped: [], failed: [] }} onClose={() => {}} />,
    );
    expect(screen.queryByText(/retry/i)).toBeNull();
    expect(screen.getByTestId('bulk-failed-count').textContent).toContain('0');
  });
});
