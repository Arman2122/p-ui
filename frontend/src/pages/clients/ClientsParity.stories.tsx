import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { ConfigProvider } from 'antd';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { getSharedWebSocketClient } from '@/api/websocket';
import { HttpUtil, Msg } from '@/utils';
import ClientsPage from './ClientsPage';
import { parityClient, parityClientPage, parityCores, parityDefaults, parityEnforced, parityExternalLinks, parityInbounds, parityLink, parityPlans } from './ClientsParity.fixtures';

function Surface({ dark, mobile }: { dark: boolean; mobile: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <div className="p-page" style={mobile ? { width: 390, maxWidth: '100%' } : undefined}><ClientsPage /></div>;
}
function ClientsWorkbench({ dark = false, mobile = false, fromPlan = false }: { dark?: boolean; mobile?: boolean; fromPlan?: boolean }) {
  const [cache] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } }));
  return <MemoryRouter initialEntries={[fromPlan ? '/clients?create=1&planId=1' : '/clients']}>
    <QueryClientProvider client={cache}><ThemeProvider><ConfigProvider theme={{ token: { motion: false } }}><Surface dark={dark} mobile={mobile} /></ConfigProvider></ThemeProvider></QueryClientProvider>
  </MemoryRouter>;
}

const meta = {
  title: 'Pages/Clients/Design parity', component: ClientsWorkbench, parameters: { layout: 'fullscreen' },
  render: (args, context) => <ClientsWorkbench {...args} dark={context.globals.theme === 'dark'} />,
  beforeEach: (context) => {
    const oldWidth = Object.getOwnPropertyDescriptor(window, 'innerWidth');
    if (context.args.mobile) Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 });
    localStorage.removeItem('clientsFilterState');
    localStorage.removeItem('clientsRowLayout');
    const socket = spyOn(getSharedWebSocketClient(), 'connect').mockImplementation(() => {});
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let obj: unknown;
      if (url.startsWith('/panel/api/clients/list/paged')) obj = parityClientPage();
      else if (url.startsWith('/panel/api/clients/get/')) obj = { client: parityClient, credentials: { uuid: parityClient.uuid, password: parityClient.password }, inboundIds: [1, 2], externalLinks: parityExternalLinks };
      else if (url === '/panel/api/cores') obj = parityCores;
      else if (url === '/panel/api/policies/list') obj = parityPlans;
      else if (url.startsWith('/panel/api/policies/enforced/')) obj = parityEnforced;
      else if (url === '/panel/api/inbounds/options') obj = parityInbounds;
      else if (url === '/panel/api/server/ipLimitStatus') obj = { enabled: true, enforceable: true, reason: '' };
      else if (url.startsWith('/panel/api/clients/subLinks/')) obj = [parityLink];
      else obj = [];
      return new Msg(true, '', obj as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string) {
      let obj: unknown;
      if (url === '/panel/api/setting/defaultSettings') obj = parityDefaults;
      else if (url === '/panel/api/clients/onlines') obj = ['roham'];
      else if (url === '/panel/api/clients/ips/roham') obj = [];
      else if (url === '/panel/api/clients/add' && context.name === 'Create And Save') obj = {};
      else if (url === '/panel/api/clients/update/roham' && context.name === 'Edit And Save') obj = {};
      else if (url === '/panel/api/clients/roham/externalLinks' && context.name === 'Edit And Save') obj = {};
      else throw new Error(`Unexpected story POST: ${url}`);
      return new Msg(true, '', obj as T);
    });
    return () => { get.mockRestore(); post.mockRestore(); socket.mockRestore(); if (oldWidth) Object.defineProperty(window, 'innerWidth', oldWidth); };
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
  await userEvent.click(await within(canvasElement).findByRole('button', { name: 'Bulk actions' }));
  await userEvent.click(await within(document.body).findByRole('menuitem', { name: /Add Bulk$/ }));
  await within(document.body).findByRole('tab', { name: 'Batch' });
} };
export const BulkFormDark: Story = { ...BulkForm, globals: { theme: 'dark' } };
export const CreateForm: Story = { args: { fromPlan: true }, play: async () => {
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
