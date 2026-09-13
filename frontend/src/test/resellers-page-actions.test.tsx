import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { expect, it, vi } from 'vitest';
import i18next from 'i18next';

import ResellersPage from '@/pages/resellers/ResellersPage';
import { renderWithProviders } from './test-utils';

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/pages/resellers/api', async () => ({
  ...await vi.importActual<typeof import('@/pages/resellers/api')>('@/pages/resellers/api'),
  resellerGet: api.get,
  resellerPost: api.post,
}));
vi.mock('@/pages/index/HistoryChart', () => ({ default: ({ series }: { series: { values: number[] }[] }) => <div data-testid="owner-history" data-values={JSON.stringify(series[0].values)} /> }));

const account = {
  userId: 7, name: 'Partner Co', email: 'partner@example.com', trafficLimit: 500 * 1024 ** 3, trafficAllocated: 0, trafficUsed: 120 * 1024 ** 3, clientCap: 50, expiresAt: Date.now() + 30 * 86400000,
  suspended: false, budgetBlocked: false, allowPlans: true, allowReset: true, allowExtend: false, allowSuspend: true, allowTopUp: true, notifyOnDepletion: true, note: '', createdAt: 1, updatedAt: 1,
  onDepletion: 'block', resetCycle: 'monthly', periodStartedAt: Date.now() - 5 * 86400000,
};
const view = { account, username: 'partner', clients: 3, trafficUsed: account.trafficUsed, inboundIds: [12], nextResetAt: Date.now() + 25 * 86400000, online: 2, active: 3, history: [{ t: Math.floor(Date.now() / 86400000) * 86400, up: 5, down: 7 }] };
const ownedClient = { id: 31, email: 'owned-one', subId: 's', totalGB: 0, expiryTime: 0, limitIp: 0, enable: true, comment: '', trafficUsed: 0, lastOnline: 0, inboundIds: [12] };

function setup() {
  api.get.mockImplementation(async (path: string) => {
    if (path === '/resellers') return [view];
    if (path === '/inbounds/list') return [{ id: 12, remark: 'Granted entry', protocol: 'vless' }];
    if (path === '/resellers/7/clients') return { items: [ownedClient], total: 1 };
    if (path === '/clients/list/paged') return { items: [{ email: 'owned-one' }, { email: 'admin-free' }] };
    throw new Error(`Unexpected request: ${path}`);
  });
  api.post.mockResolvedValue({ moved: 1, plansCleared: 0 });
  return renderWithProviders(<MemoryRouter><ResellersPage /></MemoryRouter>);
}

it('shows the account rules and counts, and drives reset, delete and release through their endpoints', async () => {
  setup();
  await screen.findByText('Partner Co', {}, { timeout: 5000 });
  expect(screen.getByText(i18next.t('resellers.policy.block'))).toBeTruthy();
  expect(screen.getByText(i18next.t('resellers.cycle.monthly'))).toBeTruthy();
  expect(screen.getByTestId('online-7').textContent).toBe('2');
  const values = JSON.parse(screen.getByTestId('owner-history').getAttribute('data-values') || '[]') as (number | null)[];
  expect(values.at(-1)).toBe(12);

  const reset = () => screen.getAllByRole('button', { name: i18next.t('resellers.resetUsage') });
  fireEvent.click(reset()[0]);
  await waitFor(() => expect(reset().length).toBe(2), { timeout: 5000 });
  fireEvent.click(reset().at(-1)!);
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/resellers/7/reset-usage', {}), { timeout: 5000 });

  fireEvent.click(screen.getByRole('button', { name: `${i18next.t('resellers.deleteReseller')}: partner` }));
  const dialog = await screen.findByRole('dialog', {}, { timeout: 5000 });
  expect(within(dialog).getByText(i18next.t('resellers.deleteHelp', { count: 3 }))).toBeTruthy();
  fireEvent.click(within(dialog).getByRole('button', { name: i18next.t('delete') }));
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/resellers/7/delete', { clients: 'transfer', ownerId: 0 }), { timeout: 5000 });

  fireEvent.click(screen.getByRole('button', { name: i18next.t('resellers.clients') }));
  await screen.findByText('owned-one', {}, { timeout: 5000 });
  fireEvent.click(screen.getByRole('button', { name: `${i18next.t('resellers.release')}: owned-one` }));
  const confirmRelease = await screen.findByRole('button', { name: i18next.t('resellers.release') }, { timeout: 5000 });
  fireEvent.click(confirmRelease);
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/resellers/7/clients/release', { emails: ['owned-one'] }), { timeout: 5000 });
  await screen.findByText(i18next.t('resellers.assigned', { moved: 1, plans: 0 }), {}, { timeout: 5000 });
}, 40000);
