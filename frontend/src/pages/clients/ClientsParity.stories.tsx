import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { ConfigProvider } from 'antd';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { getSharedWebSocketClient } from '@/api/websocket';
import { HttpUtil, IntlUtil, Msg, SizeFormatter } from '@/utils';
import ClientsPage from './ClientsPage';
import { parityClient, parityClientPage, parityCores, parityDefaults, parityEnforced, parityExternalLinks, parityInbounds, parityLink, parityPlans } from './ClientsParity.fixtures';
import { fleetClient } from './ClientsFleet.fixtures';
import { ClientCreatePayloadSchema } from '@/generated/zod';
import { creationAdmin, creationResult } from './FleetClientCreation.fixtures';

function Surface({ dark, mobile }: { dark: boolean; mobile: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <div className="p-page" style={mobile ? { width: 390, maxWidth: '100%' } : undefined}><ClientsPage /></div>;
}
function ClientsWorkbench({ dark = false, mobile = false, fromPlan = false, planFilter = false }: { dark?: boolean; mobile?: boolean; fromPlan?: boolean; planFilter?: boolean; fleet?: boolean; ownedCreate?: boolean; uncertainCreate?: boolean; mixedCipherCreate?: boolean }) {
  const [cache] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } }));
  return <MemoryRouter initialEntries={[fromPlan ? '/clients?create=1&planId=1' : planFilter ? '/clients?planId=1' : '/clients']}>
    <QueryClientProvider client={cache}><ThemeProvider><ConfigProvider theme={{ token: { motion: false } }}><Surface dark={dark} mobile={mobile} /></ConfigProvider></ThemeProvider></QueryClientProvider>
  </MemoryRouter>;
}

const meta = {
  title: 'Pages/Clients/Design parity', component: ClientsWorkbench, parameters: { layout: 'fullscreen' },
  render: (args, context) => <ClientsWorkbench {...args} dark={context.globals.theme === 'dark'} />,
  beforeEach: (context) => {
    const oldRole = window.PUI_ROLE, oldUser = window.PUI_USER;
    sessionStorage.removeItem('fleet-client-creation:1');
    let firstOperationId: string | undefined;
    const oldWidth = Object.getOwnPropertyDescriptor(window, 'innerWidth');
    if (context.args.mobile) Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    localStorage.removeItem('clientsFilterState');
    localStorage.removeItem('clientsRowLayout');
    const socket = spyOn(getSharedWebSocketClient(), 'connect').mockImplementation(() => {});
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let obj: unknown;
      if (url === '/panel/api/me') obj = creationAdmin;
      else if (url === '/panel/api/fleet/clients/capabilities') obj = { supported: Boolean(context.args.ownedCreate), actorId: creationAdmin.id };
      else if (url.startsWith('/panel/api/fleet/clients/creation/')) {
        if (url.endsWith(`/${firstOperationId}`)) obj = creationResult;
        else return new Msg<T>(false, 'No confirmed receipt');
      }
      else if (url.startsWith('/panel/api/clients/list/paged')) obj = context.args.fleet ? { ...parityClientPage(), items: [fleetClient], total: 1, filtered: 1 } : parityClientPage();
      else if (url.startsWith('/panel/api/clients/get/')) obj = context.args.fleet ? { client: fleetClient, credentials: {}, inboundIds: [], externalLinks: [] } : { client: parityClient, credentials: { uuid: parityClient.uuid, password: parityClient.password }, inboundIds: [1, 2], externalLinks: parityExternalLinks };
      else if (url === '/panel/api/cores') obj = context.args.mixedCipherCreate ? [{ ...parityCores[0], kinds: ['shadowsocks'], clientCredentials: { shadowsocks: ['password'] } }] : parityCores;
      else if (url === '/panel/api/policies/list') obj = parityPlans;
      else if (url.startsWith('/panel/api/policies/enforced/')) obj = parityEnforced;
      else if (url === '/panel/api/inbounds/options') obj = context.args.mixedCipherCreate ? parityInbounds.map((row, index) => ({ ...row, protocol: 'shadowsocks', tlsFlowCapable: false,
        ssMethod: index === 0 ? '2022-blake3-aes-128-gcm' : '2022-blake3-aes-256-gcm' })) : parityInbounds;
      else if (url === '/panel/api/server/ipLimitStatus') obj = { enabled: true, enforceable: true, reason: '' };
      else if (url.startsWith('/panel/api/clients/subLinks/')) obj = [parityLink];
      else obj = [];
      return new Msg(true, '', obj as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string, body?: unknown) {
      let obj: unknown;
      if (url === '/panel/api/setting/defaultSettings') obj = parityDefaults;
      else if (url === '/panel/api/fleet/clients' && context.args.ownedCreate) {
        const request = ClientCreatePayloadSchema.parse(body);
        if (!request.operationId) throw new Error('Missing creation operation identity');
        firstOperationId ??= request.operationId;
        if (context.args.uncertainCreate) return new Msg<T>(false, 'Unconfirmed response');
        obj = creationResult;
      }
      else if (url === '/panel/api/clients/onlines') obj = ['roham'];
      else if (url === '/panel/api/clients/ips/roham' || context.args.fleet && url === `/panel/api/clients/ips/${fleetClient.email}`) obj = [];
      else if (url === '/panel/api/clients/add' && context.name === 'Create And Save') obj = {};
      else if (url === '/panel/api/clients/update/roham' && context.name === 'Edit And Save') obj = {};
      else if (url === '/panel/api/clients/roham/externalLinks' && context.name === 'Edit And Save') obj = {};
      else if (context.args.fleet && url === '/panel/api/clients/bulkEnable') obj = { changed: 1, pending: 1, skipped: [], outcome: { ok: [fleetClient.email], skipped: [], failed: [] } };
      else throw new Error(`Unexpected story POST: ${url}`);
      return new Msg(true, '', obj as T);
    });
    return () => { get.mockRestore(); post.mockRestore(); socket.mockRestore(); sessionStorage.removeItem('fleet-client-creation:1'); window.PUI_ROLE = oldRole; window.PUI_USER = oldUser; if (oldWidth) Object.defineProperty(window, 'innerWidth', oldWidth); };
  },
} satisfies Meta<typeof ClientsWorkbench>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Page: Story = {};
export const PageDark: Story = { globals: { theme: 'dark' } };
export const WideTableFits: Story = {
  globals: { theme: 'dark' },
  parameters: { viewport: { defaultViewport: 'wide', options: { wide: { name: 'Wide desktop', styles: { width: '2305px', height: '900px' } } } } },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findAllByRole('button', { name: 'Delete' });
    function fits() {
      const content = canvasElement.querySelector<HTMLElement>('.ant-table-content')!;
      expect(content.clientWidth).toBeGreaterThanOrEqual(1260);
      expect(content.scrollWidth).toBe(content.clientWidth);
      for (const button of canvas.getAllByRole('button', { name: 'Delete' })) {
        const cell = button.closest('td')!;
        expect(cell.querySelectorAll('button')).toHaveLength(6);
        expect(cell.scrollWidth).toBeLessThanOrEqual(cell.clientWidth);
        expect(button.getBoundingClientRect().right).toBeLessThanOrEqual(content.getBoundingClientRect().right);
      }
    }
    await waitFor(fits);
    await userEvent.click(canvas.getByRole('radio', { name: 'Columns' }).closest('label')!);
    await waitFor(fits);
  },
};
export const DesktopTableFits: Story = {
  ...WideTableFits,
  parameters: { viewport: { defaultViewport: 'desktop', options: { desktop: { name: 'Desktop', styles: { width: '1600px', height: '900px' } } } } },
};
export const PageMobile: Story = { args: { mobile: true }, globals: { theme: 'dark' } };
export const BulkForm: Story = { play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await userEvent.click(await within(canvasElement).findByRole('button', { name: 'Bulk actions' }));
  await userEvent.click(await body.findByRole('menuitem', { name: /Add Bulk$/ }));
  // Cold Vite module loading can exceed the default one-second interaction wait.
  await waitFor(() => expect(body.getByRole('dialog')).toBeVisible(), { timeout: 5000 });
  await body.findByRole('tab', { name: 'Batch' });
} };
export const BulkFormDark: Story = { ...BulkForm, globals: { theme: 'dark' } };
export const CreateForm: Story = { args: { fromPlan: true }, play: async ({ canvasElement }) => {
  const body = within(canvasElement.ownerDocument.body);
  await waitFor(() => expect(body.getByRole('dialog')).toBeVisible(), { timeout: 5000 });
  await waitFor(() => expect(document.querySelector('.client-plan-summary')).toBeInTheDocument());
} };
export const CreateFormDark: Story = { ...CreateForm, globals: { theme: 'dark' } };
export const CreateFormMobile: Story = { ...CreateForm, args: { fromPlan: true, mobile: true }, globals: { theme: 'dark' } };
export const EditForm: Story = { play: async ({ canvasElement }) => {
  await userEvent.click((await within(canvasElement).findAllByRole('button', { name: 'Edit' }))[0]);
  await waitFor(() => expect(document.querySelector('.client-plan-summary')).toBeInTheDocument());
} };
export const EditFormDark: Story = { ...EditForm, globals: { theme: 'dark' } };
export const EditFormMobile: Story = { args: { mobile: true }, globals: { theme: 'dark' }, play: async ({ canvasElement }) => {
  await waitFor(() => expect(canvasElement.querySelector('.client-card .ant-dropdown-trigger')).toBeInTheDocument());
  await userEvent.click(canvasElement.querySelector<HTMLButtonElement>('.client-card .ant-dropdown-trigger')!);
  await userEvent.click(await within(document.body).findByRole('menuitem', { name: /Edit$/ }));
  await waitFor(() => expect(document.querySelector('.client-plan-summary')).toBeInTheDocument());
} };
export const CreateAndSave: Story = { args: { fromPlan: true }, play: async () => {
  const body = within(document.body);
  await waitFor(() => expect(document.querySelector('.client-plan-summary')).toBeInTheDocument());
  const name = await body.findByRole('textbox', { name: 'Email' });
  await userEvent.clear(name); await userEvent.type(name, 'new-parity-client');
  await userEvent.click(body.getByRole('tab', { name: 'Limits' }));
  expect(body.getByRole('spinbutton', { name: /Traffic Limit/ })).toBeDisabled();
  await userEvent.click(body.getByRole('tab', { name: 'Credentials' }));
  await userEvent.click(body.getByRole('button', { name: /Create$/ }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/clients/add', expect.objectContaining({
    policyId: 1, inboundIds: [1, 2], client: expect.objectContaining({ email: 'new-parity-client', totalGB: 50 * 1024 ** 3, expiryTime: -30 * 86400000, reset: 30, enable: true }),
  }), expect.anything()));
  await waitFor(() => expect(body.queryByRole('dialog')).not.toBeInTheDocument(), { timeout: 5000 });
} };
export const EditAndSave: Story = { play: async (context) => {
  await EditForm.play!(context);
  const body = within(document.body);
  await userEvent.type(body.getByRole('textbox', { name: 'Comment' }), ' · reviewed');
  await userEvent.click(body.getByRole('tab', { name: 'Links' }));
  await waitFor(() => expect(document.body.textContent).toContain(parityLink));
  await userEvent.click(body.getByRole('button', { name: /Save$/ }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/clients/update/roham', expect.objectContaining({
    id: parityClient.uuid, password: parityClient.password, subId: parityClient.subId, totalGB: parityClient.totalGB,
    flow: parityClient.flow, tgId: parityClient.tgId, inboundIds: [1, 2], policyId: undefined,
  }), expect.anything()));
  expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/clients/roham/externalLinks', { externalLinks: parityExternalLinks }, expect.anything());
  await waitFor(() => expect(body.queryByRole('dialog')).not.toBeInTheDocument(), { timeout: 5000 });
} };

function ownedCreationRequests() {
  return (HttpUtil.post as unknown as { mock: { calls: unknown[][] } }).mock.calls
    .filter(([url]) => url === '/panel/api/fleet/clients').map(([, body]) => ClientCreatePayloadSchema.parse(body));
}

export const FleetCreateWithLinks: Story = { args: { fromPlan: true, ownedCreate: true }, play: async () => {
  const body = within(document.body);
  await waitFor(() => expect(body.getByRole('dialog')).toBeVisible(), { timeout: 5000 });
  await waitFor(() => expect(body.getByRole('button', { name: /^Create$/ })).toBeEnabled());
  const email = body.getByRole('textbox', { name: 'Email' });
  await userEvent.clear(email); await userEvent.type(email, 'owned-with-links');
  await userEvent.click(body.getByText('Custom client', { selector: '.ant-segmented-item-label' }));
  await userEvent.click(body.getByRole('tab', { name: 'Credentials' }));
  const password = body.getByText('Password', { selector: 'label' }).closest('.ant-form-item')!.querySelector('input')!;
  expect(password).toHaveValue('');
  await userEvent.type(password, 'explicit-owned-password');
  await userEvent.click(body.getByRole('tab', { name: 'Links' }));
  await userEvent.click(body.getByRole('button', { name: /Add External Link$/ }));
  await userEvent.type(body.getByRole('textbox', { name: 'vless:// · vmess:// · trojan:// · ss:// · hysteria2:// · wireguard://' }), parityLink);
  await userEvent.click(body.getByRole('button', { name: /^Create$/ }));
  await waitFor(() => expect(ownedCreationRequests()).toHaveLength(1));
  expect(ownedCreationRequests()[0]).toMatchObject({ client: { email: 'owned-with-links', password: 'explicit-owned-password' },
    externalLinks: [{ kind: 'link', value: parityLink, remark: '' }] });
  expect(ownedCreationRequests()[0].policyId ?? 0).toBe(0);
  expect((HttpUtil.post as unknown as { mock: { calls: unknown[][] } }).mock.calls.some(([url]) => url === '/panel/api/clients/add' || String(url).includes('/externalLinks'))).toBe(false);
  await waitFor(() => expect(body.queryByRole('dialog')).not.toBeInTheDocument(), { timeout: 5000 });
} };
export const FleetCreateWithLinksDark: Story = { ...FleetCreateWithLinks, globals: { theme: 'dark' } };

export const FleetCreateUnconfirmedStartNew: Story = { args: { fromPlan: true, ownedCreate: true, uncertainCreate: true }, play: async () => {
  const body = within(document.body);
  await waitFor(() => expect(body.getByRole('dialog')).toBeVisible(), { timeout: 5000 });
  await waitFor(() => expect(body.getByRole('button', { name: /^Create$/ })).toBeEnabled());
  const email = body.getByRole('textbox', { name: 'Email' });
  await userEvent.clear(email); await userEvent.type(email, 'unconfirmed-owned');
  await userEvent.click(body.getByRole('button', { name: /^Create$/ }));
  await body.findByText('Client creation is unconfirmed');
  const first = ownedCreationRequests()[0];
  await userEvent.click(body.getByRole('button', { name: 'Start a new request' }));
  await body.findByText('Start a new creation request?');
  expect(JSON.parse(sessionStorage.getItem('fleet-client-creation:1')!)).toMatchObject({ activeOperationId: first.operationId });
  await userEvent.click(within(document.querySelector('.ant-popconfirm')!).getByRole('button', { name: 'Start a new request' }));
  await waitFor(() => expect(body.queryByText('Client creation is unconfirmed')).not.toBeInTheDocument());
  expect(body.getByRole('textbox', { name: 'Email' })).toHaveValue(first.client.email);
  await userEvent.click(body.getByRole('button', { name: /^Create$/ }));
  await body.findByText('Client creation is unconfirmed');
  const second = ownedCreationRequests()[1];
  expect(second.operationId).not.toBe(first.operationId);
  expect(second.client.email).toBe(first.client.email);
  expect(second.client.subId).toBe(first.client.subId);
  expect(JSON.parse(sessionStorage.getItem('fleet-client-creation:1')!)).toMatchObject({ activeOperationId: second.operationId, operations: [first.operationId, second.operationId] });
  await userEvent.click(body.getByRole('button', { name: `Check receipt ${first.operationId}` }));
  await waitFor(() => expect(JSON.parse(sessionStorage.getItem('fleet-client-creation:1')!)).toMatchObject({ activeOperationId: second.operationId, operations: [second.operationId] }));
  expect(body.getByText('Client creation is unconfirmed')).toBeVisible();
  await userEvent.click(body.getByRole('button', { name: 'Retry the same request' }));
  await waitFor(() => expect(ownedCreationRequests()).toHaveLength(3));
  expect(ownedCreationRequests()[2]).toEqual(second);
} };

export const FleetCreatePreservesExplicitCipherCredential: Story = { args: { fromPlan: true, ownedCreate: true, mixedCipherCreate: true }, play: async () => {
  const body = within(document.body);
  await waitFor(() => expect(body.getByRole('dialog')).toBeVisible(), { timeout: 5000 });
  await waitFor(() => expect(body.getByRole('button', { name: /^Create$/ })).toBeEnabled());
  await userEvent.click(body.getByText('Custom client', { selector: '.ant-segmented-item-label' }));
  await userEvent.click(body.getByRole('tab', { name: 'Credentials' }));
  const password = body.getByText('Password', { selector: 'label' }).closest('.ant-form-item')!.querySelector('input')!;
  expect(password).toHaveValue('');
  const supplied = 'AQEBAQEBAQEBAQEBAQEBAQ==';
  await userEvent.type(password, supplied);
  await userEvent.click(body.getByRole('tab', { name: 'Basics' }));
  const input = body.getByRole('combobox', { name: 'Attached inbounds' });
  const select = input.closest('.ant-select')!;
  await userEvent.click(input);
  await userEvent.click(await body.findByText(/Game edge/, { selector: '.ant-select-item-option-content' }));
  await waitFor(() => expect(select).not.toHaveTextContent('Game edge'));
  await userEvent.click(body.getByRole('tab', { name: 'Credentials' }));
  expect(body.getByText('Password', { selector: 'label' }).closest('.ant-form-item')!.querySelector('input')).toHaveValue(supplied);
} };
export const PlanFilterFromLink: Story = {
  args: { planFilter: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await canvas.findByText('Plan: Standard 50');
    const reads = (HttpUtil.get as unknown as { mock: { calls: unknown[][] } }).mock.calls;
    await waitFor(() => expect(reads.some(([url]) => String(url).includes('policyId=1'))).toBe(true));
  },
};

export const FleetLedgerPendingEdit: Story = {
  args: { fleet: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const rowElement = () => canvas.getByRole('button', { name: fleetClient.email }).closest('tr')!;
    await waitFor(() => {
      const row = within(rowElement());
      expect(row.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '30');
      expect(row.getByRole('progressbar')).toHaveAttribute('aria-label', `${SizeFormatter.sizeFormat(3 * 1024 ** 3)} / ${SizeFormatter.sizeFormat(10 * 1024 ** 3)}`);
      expect(row.getByText(IntlUtil.formatDate(2208988800000))).toBeVisible();
      expect(row.getByRole('button', { name: 'Active' })).toBeVisible();
      expect(row.getByRole('img', { name: 'Reported daily totals' })).toBeVisible();
    });
    await userEvent.click(rowElement().querySelector('.client-traffic-cell')!);
    const body = within(canvasElement.ownerDocument.body);
    await waitFor(() => expect(body.getByText('Measured traffic')).toBeVisible());
    expect(body.getByText('Uncertain usage').closest('tr')).toHaveTextContent('Unknown');
    expect(body.getByText('Reserved allowance').closest('tr')).toHaveTextContent(SizeFormatter.sizeFormat(4 * 1024 ** 3));
    await userEvent.click(within(rowElement()).getByRole('button', { name: 'Edit' }));
    const dialog = within(await body.findByRole('dialog'));
    await userEvent.click(dialog.getByRole('tab', { name: 'Limits' }));
    expect(dialog.getByRole('spinbutton', { name: /Traffic Limit/ })).toHaveValue('1');
  },
};

export const FleetEnablePending: Story = {
  args: { fleet: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click((await canvas.findByRole('radio', { name: 'Columns' })).closest('label')!);
    const name = await canvas.findByRole('button', { name: fleetClient.email });
    await userEvent.click(within(name.closest('tr')!).getByRole('switch'));
    expect(await within(document.body).findByText('Changes saved for 1 clients; 1 awaiting node confirmation')).toBeVisible();
    expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/clients/bulkEnable', { emails: [fleetClient.email] }, expect.anything());
  },
};

export const FleetBulkEnablePending: Story = {
  args: { fleet: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const name = await canvas.findByRole('button', { name: fleetClient.email });
    await userEvent.click(within(name.closest('tr')!).getByRole('checkbox'));
    await userEvent.click(canvas.getByRole('button', { name: 'Bulk actions' }));
    const body = within(document.body);
    await userEvent.click(await body.findByRole('menuitem', { name: /Enable$/ }));
    await userEvent.click(within(await body.findByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    expect(await body.findByText('Changes saved for 1 clients; 1 awaiting node confirmation')).toBeVisible();
    expect(body.queryByText('1 clients enabled')).not.toBeInTheDocument();
  },
};
