import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { HttpUtil, Msg } from '@/utils';
import type { GroupSummary } from '@/schemas/client';
import { planParityInbounds } from '../policies/PlansParity.fixtures';
import GroupFormModal from './GroupFormModal';
import GroupsPage from './GroupsPage';

const written = fn();
const text = (key: string) => String(i18next.t(key));
function fixtures(): GroupSummary[] {
  const today = Math.floor(Date.now() / 86400000) * 86400;
  return [
    { name: 'Family', quotaBytes: 500 * 1024 ** 3, trafficUsed: 180 * 1024 ** 3, up: 20 * 1024 ** 3, down: 160 * 1024 ** 3, clientCount: 12, onlineCount: 4, expiringCount: 2, inboundIds: [11, 12, 13], sharedInboundIds: [11, 12], history: Array.from({ length: 14 }, (_, index) => ({ t: today - (13 - index) * 86400, up: (index + 1) * 1048576, down: (index % 4 + 1) * 1073741824 })) },
    { name: 'Friends', quotaBytes: 100 * 1024 ** 3, trafficUsed: 94 * 1024 ** 3, up: 14 * 1024 ** 3, down: 80 * 1024 ** 3, clientCount: 8, onlineCount: 3, expiringCount: 1, inboundIds: [11], sharedInboundIds: [11], history: [] },
    { name: 'Unlimited', quotaBytes: 0, trafficUsed: 10 * 1024 ** 3, up: 2 * 1024 ** 3, down: 8 * 1024 ** 3, clientCount: 3, onlineCount: 1, expiringCount: 0, inboundIds: [13], sharedInboundIds: [13], history: [] },
  ];
}
function GroupCatalogue({ form = 'none', mobile = false }: { form?: 'none' | 'create' | 'edit'; mobile?: boolean }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } }));
  const [open, setOpen] = useState(form !== 'none');
  useEffect(() => () => client.clear(), [client]);
  return <MemoryRouter><QueryClientProvider client={client}><main className="p-page" style={mobile ? { width: 390, maxWidth: '100%' } : undefined}>
    {form === 'none' || !open ? <GroupsPage /> : <GroupFormModal group={form === 'edit' ? fixtures()[0] : null} groups={fixtures()} inbounds={planParityInbounds} onClose={() => setOpen(false)} onSaved={() => { void client.invalidateQueries(); }} />}
  </main></QueryClientProvider></MemoryRouter>;
}
const meta = {
  title: 'Pages/Groups', component: GroupCatalogue, parameters: { layout: 'fullscreen' },
  play: async ({ args }) => {
    if (args.form && args.form !== 'none') await waitFor(() => expect(within(document.body).getByRole('dialog')).toBeVisible());
  },
  beforeEach: () => {
    let groups = fixtures(); written.mockClear();
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let obj: unknown;
      if (url.endsWith('/clients/groups')) obj = groups;
      else if (url.endsWith('/inbounds/options')) obj = planParityInbounds;
      else if (url.includes('default')) obj = {};
      else if (url.endsWith('/clients/list')) obj = [{ email: 'mina@example.test', group: 'Family' }, { email: 'ali@example.test', group: '' }];
      else if (url.endsWith('/emails')) obj = ['mina@example.test'];
      else throw new Error(`Unexpected story read: ${url}`);
      return new Msg(true, '', obj as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string, body?: unknown) {
      written(url, body);
      const values = body as { name?: string; oldName?: string; newName?: string; quotaBytes?: number; inboundIds?: number[] };
      if (url.endsWith('/create')) groups = [...groups, { name: values.name!, clientCount: 0, trafficUsed: 0, up: 0, down: 0 }];
      else if (url.endsWith('/rename')) groups = groups.map((group) => group.name === values.oldName ? { ...group, name: values.newName! } : group);
      else if (url.endsWith('/quota')) groups = groups.map((group) => group.name === values.name ? { ...group, quotaBytes: values.quotaBytes, sharedInboundIds: values.inboundIds ?? group.sharedInboundIds } : group);
      else if (url.endsWith('/bulkAdd') || url.endsWith('/bulkRemove')) return new Msg(true, '', { affected: 1 } as T);
      else throw new Error(`Unexpected story write: ${url}`);
      return new Msg<T>(true, '');
    });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof GroupCatalogue>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Catalogue: Story = {};
export const CatalogueDark: Story = { globals: { theme: 'dark' } };
export const CatalogueMobile: Story = { args: { mobile: true } };
export const CreateForm: Story = { args: { form: 'create' } };
export const CreateFormDark: Story = { ...CreateForm, globals: { theme: 'dark' } };
export const CreateFormMobile: Story = { ...CreateForm, globals: { viewport: { value: 'mobile1', isRotated: false } } };
export const EditForm: Story = { args: { form: 'edit' } };
export const EditFormDark: Story = { ...EditForm, globals: { theme: 'dark' } };
export const EditFormMobile: Story = { ...EditForm, globals: { viewport: { value: 'mobile1', isRotated: false } } };
export const CreateSave: Story = { ...CreateForm, play: async () => {
  const dialog = within(await within(document.body).findByRole('dialog'));
  await userEvent.type(dialog.getByRole('textbox', { name: text('pages.groups.name') }), 'Browser group');
  await userEvent.click(dialog.getByRole('button', { name: '100' }));
  await userEvent.click(dialog.getByRole('button', { name: text('plans.selectAll') }));
  await userEvent.click(dialog.getByRole('button', { name: text('create') }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/clients/groups/create', { name: 'Browser group' }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/clients/groups/quota', { name: 'Browser group', quotaBytes: 100 * 1024 ** 3, inboundIds: [11, 12, 13] }));
  await waitFor(() => expect(within(document.body).queryByRole('dialog')).toBeNull());
} };
export const EditSave: Story = { ...EditForm, play: async () => {
  const dialog = within(await within(document.body).findByRole('dialog'));
  const name = dialog.getByRole('textbox', { name: text('pages.groups.name') });
  await userEvent.clear(name); await userEvent.type(name, 'Family renamed');
  await userEvent.click(dialog.getByRole('button', { name: '1000' }));
  await userEvent.click(dialog.getByRole('button', { name: text('save') }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/clients/groups/rename', { oldName: 'Family', newName: 'Family renamed' }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/clients/groups/quota', { name: 'Family renamed', quotaBytes: 1000 * 1024 ** 3 }));
  await waitFor(() => expect(within(document.body).queryByRole('dialog')).toBeNull());
} };
export const Details: Story = { play: async ({ canvasElement }) => {
  await waitFor(() => expect(canvasElement.querySelector('.groups-card')).not.toBeNull());
  const card = canvasElement.querySelector('.groups-card') as HTMLElement;
  await userEvent.click(within(card).getByRole('button', { name: `${text('more')}: Family` }));
  await userEvent.click(await within(document.body).findByText(text('groupParity.details')));
  await waitFor(() => expect(within(document.body).getByRole('dialog')).toBeVisible());
} };
