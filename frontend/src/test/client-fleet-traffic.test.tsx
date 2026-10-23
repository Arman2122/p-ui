import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, renderHook, screen, waitFor, within } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import type { ReactNode } from 'react';
import ClientTrafficCell from '@/components/clients/ClientTrafficCell';
import ClientsPage from '@/pages/clients/ClientsPage';
import { ClientTrendCell } from '@/pages/clients/ClientMetadataCells';
import ClientInfoModal from '@/pages/clients/ClientInfoModal';
import { fleetClient, fleetClientWire } from '@/pages/clients/ClientsFleet.fixtures';
import { parityClientPage, parityDefaults } from '@/pages/clients/ClientsParity.fixtures';
import { useClients } from '@/hooks/useClients';
import { getSharedWebSocketClient } from '@/api/websocket';
import { HttpUtil, IntlUtil, Msg, SizeFormatter } from '@/utils';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

const chart = vi.hoisted(() => vi.fn());
vi.mock('@/pages/index/HistoryChart', () => ({ default: (props: unknown) => { chart(props); return <span data-testid="daily-chart" />; } }));
afterEach(() => vi.restoreAllMocks());

function mockPage() {
  vi.spyOn(getSharedWebSocketClient(), 'connect').mockImplementation(() => {});
  const page = { ...parityClientPage(), items: [fleetClient], total: 1, filtered: 1 };
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => new Msg(true, '', url.includes('/clients/list/paged') ? page : []));
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string) => new Msg(true, '', url.includes('/setting/defaultSettings') ? parityDefaults : []));
  return page;
}

describe('fleet traffic display', () => {
  it('uses authorized allowance and expiry for row status while source edits are pending', async () => {
    mockPage();
    const { container } = renderWithProviders(<MemoryRouter><ClientsPage /></MemoryRouter>);
    const name = await screen.findByRole('button', { name: fleetClient.email });
    const row = within(name.closest('tr')!);
    expect(row.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('30');
    expect(row.getByRole('progressbar').getAttribute('aria-label')).toContain(SizeFormatter.sizeFormat(10 * 1024 ** 3));
    expect(row.getByText(IntlUtil.formatDate(2208988800000, 'gregorian'))).toBeTruthy();
    expect(row.queryByText(IntlUtil.formatDate(1, 'gregorian'))).toBeNull();
    expect(container.querySelector('.p-pip')?.getAttribute('aria-label')).toContain('Active');
    expect(row.getByRole('img', { name: 'Reported daily totals' })).toBeTruthy();
  });

  it('separates measured usage, opening balance, reservations and unknown amounts', async () => {
    const traffic = fleetClient.traffic!;
    const { container } = renderWithProviders(<ClientTrafficCell {...traffic} enabled={traffic.enable} />);
    expect(screen.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('30');
    fireEvent.click(container.querySelector('.client-traffic-cell')!);
    const details = within(await screen.findByRole('table'));
    expect(details.getByText('Measured traffic').closest('tr')?.textContent).toContain(`↓ ${SizeFormatter.sizeFormat(2 * 1024 ** 3)}`);
    expect(details.getByText('Opening balance').closest('tr')?.textContent).toContain(`↑ ${SizeFormatter.sizeFormat(1024 ** 3)}`);
    expect(details.getByText('Reserved allowance').closest('tr')?.textContent).toContain(SizeFormatter.sizeFormat(4 * 1024 ** 3));
    expect(details.getByText('Uncertain usage').closest('tr')?.textContent).toContain('Unknown');
    expect(details.getByText('Last report received').closest('tr')?.textContent).toContain('Unknown');
    expect(details.getByText('Remaining').closest('tr')?.textContent).toContain(SizeFormatter.sizeFormat(7 * 1024 ** 3));
  });

  it('does not represent missing fleet quota as unlimited or missing reservations as zero', async () => {
    const { container } = renderWithProviders(<ClientTrafficCell up={0} accounting={{ ...fleetClientWire.traffic.accounting, reservedBytes: null }} />);
    expect(screen.queryByRole('progressbar')).toBeNull();
    expect(screen.queryByRole('img', { name: 'Unlimited' })).toBeNull();
    expect(container.querySelector('.client-traffic-cell-limit')?.textContent).toBe('Unknown');
    fireEvent.click(container.querySelector('.client-traffic-cell')!);
    const details = within(await screen.findByRole('table'));
    expect(details.getByText('Reserved allowance').closest('tr')?.textContent).toContain('Unknown');
  });

  it('keeps reported daily history gaps unknown and real zeroes as zero', () => {
    renderWithProviders(<ClientTrendCell client={fleetClient} hint="Hourly traffic" />);
    expect(screen.getByRole('img', { name: 'Reported daily totals' })).toBeTruthy();
    const props = chart.mock.lastCall?.[0] as { times: number[]; series: { values: number[] }[] };
    expect(props.times).toHaveLength(14);
    expect(props.times[1] - props.times[0]).toBe(86400);
    expect(props.series[0].values.slice(0, -2).every(Number.isNaN)).toBe(true);
    expect(props.series[0].values.slice(-2)).toEqual([1024 ** 3, 0]);
  });

  it('shows the same authorized allowance and expiry in client information', async () => {
    renderWithProviders(<ClientInfoModal open client={fleetClient} inboundsById={{}} isOnline={false} onOpenChange={() => {}} />);
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByRole('progressbar').getAttribute('aria-valuenow')).toBe('30');
    expect(dialog.getByText(IntlUtil.formatDate(2208988800000, 'gregorian'))).toBeTruthy();
    expect(dialog.queryByText(IntlUtil.formatDate(1, 'gregorian'))).toBeNull();
  });

  it('retains live accounting updates without replacing canonical editor fields', async () => {
    mockPage();
    const cache = makeTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={cache}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useClients(), { wrapper });
    act(() => result.current.setQuery({ page: 1, pageSize: 25 }));
    await waitFor(() => expect(result.current.clients).toHaveLength(1));
    const accounting = { ...fleetClientWire.traffic.accounting, reservedBytes: 0, uncertainBytes: 9, reportedUnixMilli: 1234 };
    act(() => result.current.applyClientStatsEvent({ clients: [{ email: fleetClient.email, accounting, total: 20 * 1024 ** 3, expiryTime: 2300000000000 }] }));
    await waitFor(() => expect(result.current.clients[0].traffic?.accounting).toEqual(accounting));
    const row = result.current.clients[0];
    expect(row.traffic?.accounting).toEqual(accounting);
    expect(row.traffic?.total).toBe(20 * 1024 ** 3);
    expect(row.totalGB).toBe(1024 ** 3);
    expect(row.expiryTime).toBe(1);
    expect(row.enable).toBe(false);
  });
});
