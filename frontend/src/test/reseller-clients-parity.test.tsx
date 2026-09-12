import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { expect, it, vi } from 'vitest';
import i18next from 'i18next';

import ResellerClientsPage from '@/pages/resellers/ResellerClientsPage';
import { setDatepicker } from '@/hooks/useDatepicker';
import { renderWithProviders } from './test-utils';

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/pages/resellers/api', async () => ({
  ...await vi.importActual<typeof import('@/pages/resellers/api')>('@/pages/resellers/api'),
  resellerGet: api.get,
  resellerPost: api.post,
  useSignedInUser: () => ({ data: { id: 3, role: 'reseller', permissions: { plans: true, reset: true, extend: true, suspend: true, topUp: true } } }),
}));
vi.mock('@/pages/index/HistoryChart', () => ({ default: ({ series }: { series: { values: number[] }[] }) => <div data-testid="owned-history" data-values={JSON.stringify(series[0].values)} /> }));

it('renders the compact owned-client composition with real metadata and scoped actions', async () => {
  setDatepicker('gregorian');
  const row = {
    id: 5, email: 'owned-client', subId: 'owned-sub', enable: true, comment: 'Owned account', totalGB: 10737418240,
    expiryTime: Date.now() + 30 * 86400000, limitIp: 2, trafficUsed: 1073741824, lastOnline: 0, inboundIds: [12, 99],
    plan: { policyId: 7, name: 'Private monthly', pending: true, overridden: true, unresolved: false },
    history: [{ t: Math.floor(Date.now() / 86400000) * 86400, up: 7, down: 11 }],
  };
  api.get.mockImplementation(async (path: string) => {
    if (path === '/reseller/clients') return { items: [row], total: 1 };
    if (path === '/reseller/inbounds') return [{ id: 12, remark: 'Granted entry', protocol: 'vless', enable: true, clients: 1, trafficUsed: row.trafficUsed }];
    if (path === '/reseller/plans/list') return [];
    if (path === '/reseller/clients/5/links') return ['vless://owned-fixture'];
    throw new Error(`Unexpected unscoped request: ${path}`);
  });
  api.post.mockResolvedValue({ pending: false });
  renderWithProviders(<MemoryRouter><ResellerClientsPage /></MemoryRouter>);

  await screen.findByRole('button', { name: 'owned-client' });
  const headers = screen.getAllByRole('columnheader').map((node) => node.textContent);
  expect(headers).toEqual(['pages.clients.client', 'telemetry.plan', 'pages.clients.traffic', 'telemetry.trend14d', 'lastOnline', 'resellers.expiry', 'resellers.actions'].map((key) => i18next.t(key)));
  expect(screen.getByText('Private monthly')).toBeTruthy();
  expect(screen.getByText(i18next.t('telemetry.planPending'))).toBeTruthy();
  expect(screen.getByText(i18next.t('telemetry.planOverride'))).toBeTruthy();
  await screen.findByText('Granted entry');
  expect(screen.queryByText('#99')).toBeNull();
  const chartValues = JSON.parse(screen.getByTestId('owned-history').getAttribute('data-values') || '[]') as (number | null)[];
  expect(chartValues.at(-1)).toBe(18);
  expect(chartValues.slice(0, -1)).toEqual(Array(13).fill(null));
  const bodyRow = screen.getByRole('button', { name: 'owned-client' }).closest('tr');
  expect(bodyRow).not.toBeNull();
  const cells = within(bodyRow!).getAllByRole('cell');
  expect(cells[2].querySelector('.client-traffic-cell')).not.toBeNull();
  expect(cells[4].textContent).toContain(i18next.t('offline'));
  expect(cells[4].textContent).toContain(i18next.t('telemetry.noReadings'));
  expect(cells[4].textContent).not.toMatch(/0\s*[bBkKMG]?[bB]?\/s/);

  fireEvent.click(screen.getByRole('button', { name: `${i18next.t('resellers.links')}: owned-client` }));
  await screen.findByText('vless://owned-fixture');
  expect(api.get).toHaveBeenCalledWith('/reseller/clients/5/links', expect.anything());
  expect(api.get.mock.calls.every(([path]) => String(path).startsWith('/reseller/'))).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: i18next.t('close') }));
  fireEvent.click(screen.getByRole('switch', { name: `${i18next.t('pages.clients.enabled')}: owned-client` }));
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/reseller/clients/5/update', expect.objectContaining({ email: 'owned-client', enable: false })));
});
