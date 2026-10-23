import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import { ThemeProvider } from '@/hooks/useTheme';
import { ResetScheduleCancelRequestSchema, ResetScheduleRequestSchema, type ResetScheduleView } from '@/generated/zod';
import { HttpUtil, Msg } from '@/utils';
import ClientInfoModal from './ClientInfoModal';
import { resetAdmin, resetClient, resetClientId, resetOperationId, resetView } from './FleetResetSchedule.fixtures';
import { parityDefaults } from './ClientsParity.fixtures';

const path = `/panel/api/fleet/clients/${resetClientId}/reset-schedule`;
function Workbench(_props: { pending?: boolean; uncertain?: boolean; reseller?: boolean; override?: boolean; unavailable?: boolean }) {
  const [cache] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }));
  return <QueryClientProvider client={cache}><ThemeProvider><ConfigProvider theme={{ token: { motion: false } }}>
    <ClientInfoModal open client={resetClient} inboundsById={{}} isOnline={false} onOpenChange={() => {}} />
  </ConfigProvider></ThemeProvider></QueryClientProvider>;
}

const meta = {
  title: 'Pages/Clients/Shared reset schedule', component: Workbench, parameters: { layout: 'fullscreen' },
  beforeEach: context => {
    const previousRole = window.PUI_ROLE;
    const previousUser = window.PUI_USER;
    sessionStorage.removeItem(`fleet-reset-request:1:${resetClientId}`);
    window.PUI_ROLE = 'admin';
    let current: ResetScheduleView = { ...resetView, ...(context.args.override ? { inherited: false, cycle: 'daily' } : {}),
      ...(context.args.pending ? { pendingOperationId: resetOperationId, diagnostic: 'SOURCE_CHANGED' } : {}) };
    let attempts = 0;
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      if (url === '/panel/api/me') return new Msg(true, '', { ...resetAdmin, role: context.args.reseller ? 'reseller' : 'admin' } as T);
      if (url === path) return context.args.unavailable ? new Msg<T>(false, 'Runtime unavailable') : new Msg(true, '', current as T);
      return new Msg(true, '', [] as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string, body: unknown) {
      if (url === '/panel/api/setting/defaultSettings') return new Msg(true, '', parityDefaults as T);
      if (context.args.uncertain && attempts++ === 0) return new Msg<T>(false, 'Response lost');
      if (url === path) {
        const request = ResetScheduleRequestSchema.parse(body);
        current = { ...current, revision: current.revision + 1, inherited: !request.override, cycle: request.override ? request.cycle! : 'weekly' };
      } else if (url === `${path}/cancel`) {
        const request = ResetScheduleCancelRequestSchema.parse(body);
        if (request.pendingOperationId !== current.pendingOperationId) throw new Error('Wrong pending operation');
        current = { ...current, revision: current.revision + 1, pendingOperationId: undefined, diagnostic: 'RESET_CANCELLED' };
      } else throw new Error(`Unexpected story POST: ${url}`);
      return new Msg(true, '', current as T);
    });
    return () => { get.mockRestore(); post.mockRestore(); window.PUI_ROLE = previousRole; window.PUI_USER = previousUser; sessionStorage.removeItem(`fleet-reset-request:1:${resetClientId}`); };
  },
} satisfies Meta<typeof Workbench>;
export default meta;
type Story = StoryObj<typeof meta>;

async function choose(body: ReturnType<typeof within>, policy: string) {
  await waitFor(() => expect(body.getByRole('combobox', { name: 'Reset policy' })).toBeEnabled());
  await userEvent.click(body.getByRole('combobox', { name: 'Reset policy' }));
  await userEvent.click(await body.findByText(policy, { selector: '.ant-select-item-option-content' }));
}

function requests() {
  return (HttpUtil.post as unknown as { mock: { calls: unknown[][] } }).mock.calls.filter(([url]) => typeof url === 'string' && url.startsWith(path));
}

export const SaveSchedule: Story = { play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await choose(body, 'Daily');
  expect(body.getByText('Cadence anchor (UTC)').closest('tr')).toHaveTextContent('09:30');
  await userEvent.click(body.getByRole('button', { name: 'Save reset schedule' }));
  await waitFor(() => expect(body.getByText('Schedule change accepted')).toBeVisible());
  expect(HttpUtil.post).toHaveBeenCalledWith(path, expect.objectContaining({ clientId: resetClientId, expectedRevision: 7, override: true, cycle: 'daily' }), { silent: true });
} };

export const SaveScheduleDark: Story = { ...SaveSchedule, globals: { theme: 'dark' } };

export const InheritAppliedPlan: Story = { args: { override: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await choose(body, 'Inherit applied plan');
  await userEvent.click(body.getByRole('button', { name: 'Save reset schedule' }));
  await body.findByText('From applied plan');
  const calls = requests();
  expect(calls[0][1]).not.toHaveProperty('cycle');
  expect(calls[0][1]).toMatchObject({ override: false, expectedRevision: 7 });
} };

export const UnconfirmedRequest: Story = { args: { uncertain: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await choose(body, 'Daily');
  await userEvent.click(body.getByRole('button', { name: 'Save reset schedule' }));
  await body.findByText('Request outcome unconfirmed');
  expect(body.getByRole('combobox', { name: 'Reset policy' })).toBeDisabled();
  expect(requests()).toHaveLength(1);
  const calls = requests();
  const first = structuredClone(calls[0][1]);
  await userEvent.click(body.getByRole('button', { name: 'Retry same request' }));
  await body.findByText('Schedule change accepted');
  expect(requests()[1]).toEqual([path, first, { silent: true }]);
} };

export const CancelPendingReset: Story = { args: { pending: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await waitFor(() => expect(body.getByRole('button', { name: 'Cancel pending reset' })).toBeEnabled());
  expect(body.getByRole('combobox', { name: 'Reset policy' })).toBeDisabled();
  expect(requests()).toHaveLength(0);
  await userEvent.click(body.getByRole('button', { name: 'Cancel pending reset' }));
  await waitFor(() => expect(body.getByText('Cancel this pending reset?')).toBeVisible());
  expect(body.getByText(/Keep the current allowance, used traffic and reservations/)).toBeVisible();
  expect(requests()).toHaveLength(0);
  await userEvent.click(body.getByRole('button', { name: 'Cancel this reset' }));
  await waitFor(() => expect(body.queryByRole('button', { name: 'Cancel pending reset' })).not.toBeInTheDocument());
  expect(HttpUtil.post).toHaveBeenCalledWith(`${path}/cancel`, expect.objectContaining({ clientId: resetClientId, expectedRevision: 7, pendingOperationId: resetOperationId }), { silent: true });
} };

export const ResellerCannotManageSchedule: Story = { args: { reseller: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await waitFor(() => expect(window.PUI_ROLE).toBe('reseller'));
  expect(body.queryByRole('region', { name: 'Shared reset schedule' })).not.toBeInTheDocument();
  const calls = (HttpUtil.get as unknown as { mock: { calls: unknown[][] } }).mock.calls;
  expect(calls.some(([url]) => url === path)).toBe(false);
} };

export const UnavailableRuntime: Story = { args: { unavailable: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await body.findByText('Unable to load the reset schedule');
  expect(body.queryByRole('button', { name: 'Save reset schedule' })).not.toBeInTheDocument();
  expect(requests()).toHaveLength(0);
} };
