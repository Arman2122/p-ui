import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import ClientTrafficCell from '@/components/clients/ClientTrafficCell';
import { renderWithProviders } from '@/test/test-utils';
import { SizeFormatter } from '@/utils';

const GB = 1024 ** 3;

describe('client traffic popover', () => {
  it('keeps real upload, download and remaining amounts for the default compact cell', async () => {
    const { container } = renderWithProviders(<ClientTrafficCell compact up={GB} down={2 * GB} total={5 * GB} />);
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('60');
    fireEvent.click(container.querySelector('.client-traffic-cell')!);
    const details = within(await screen.findByRole('table'));
    expect(details.getByText('↑')).toBeTruthy();
    expect(details.getByText('↓')).toBeTruthy();
    expect(details.getByText(SizeFormatter.sizeFormat(GB))).toBeTruthy();
    expect(details.getAllByText(SizeFormatter.sizeFormat(2 * GB))).toHaveLength(2);
  });

  it.each([{ used: 3 * GB, percent: '60' }, { used: 0, percent: '0' }])('uses aggregate $used without inventing a directional split', async ({ used, percent }) => {
    const { container } = renderWithProviders(<ClientTrafficCell compact usedTotal={used} up={9 * GB} down={9 * GB} total={5 * GB} />);
    expect(container.querySelector('.client-traffic-cell-used')?.textContent).toBe(SizeFormatter.sizeFormat(used));
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe(percent);
    fireEvent.click(container.querySelector('.client-traffic-cell')!);
    const details = within(await screen.findByRole('table'));
    expect(details.queryByText('↑')).toBeNull();
    expect(details.queryByText('↓')).toBeNull();
    expect(details.getByText(SizeFormatter.sizeFormat(used))).toBeTruthy();
    expect(details.getByText(SizeFormatter.sizeFormat(5 * GB - used))).toBeTruthy();
  });
});
