import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { HttpUtil, Msg } from '@/utils';
import { PlanRecordSchema } from '@/schemas/api/policy';
import { planParityFixtures, planParityInbounds, planParityMembers } from './PlansParity.fixtures';
import PlansPage from './PlansPage';
import PolicyFormModal from './PolicyFormModal';
import '@/styles/design-pages.css';

const written = fn();
const text = (key: string) => String(i18next.t(key));

function Catalogue({ form = 'none', mobile = false }: { form?: 'none' | 'create' | 'edit'; mobile?: boolean }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } }));
  const [open, setOpen] = useState(form !== 'none');
  useEffect(() => () => client.clear(), [client]);
  return <MemoryRouter><QueryClientProvider client={client}><main className="p-page" style={mobile ? { width: 390, maxWidth: '100%', containerType: 'inline-size', containerName: 'main' } : undefined}>
    {form === 'none' || !open ? <PlansPage /> : <PolicyFormModal open policy={form === 'edit' ? planParityFixtures()[0] : null} plans={planParityFixtures()} inbounds={planParityInbounds} onClose={() => setOpen(false)} />}
  </main></QueryClientProvider></MemoryRouter>;
}

const meta = {
  title: 'Pages/Plans', component: Catalogue, parameters: { layout: 'fullscreen' },
  play: async ({ args }) => {
    if (args.form && args.form !== 'none') await waitFor(() => expect(within(document.body).getByRole('dialog')).toBeVisible());
  },
  beforeEach: () => {
    let plans = planParityFixtures(); written.mockClear();
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let obj: unknown;
      if (url.endsWith('/policies/list')) obj = plans;
      else if (url.endsWith('/inbounds/options')) obj = planParityInbounds;
      else if (url.endsWith('/cores')) obj = [];
      else if (url.endsWith('/summary')) obj = { withoutPlan: 4 };
      else if (url.includes('/members/')) obj = planParityMembers(Number(url.split('/').at(-1)));
      else throw new Error(`Unexpected story read: ${url}`);
      return new Msg(true, '', obj as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string, body?: unknown) {
      written(url, body);
      if (url.endsWith('/add')) plans = [...plans, PlanRecordSchema.parse({ id: 4, rev: 1, createdAt: 0, updatedAt: 0, ...(body as object) })];
      else if (url.includes('/update/')) plans = plans.map((plan) => plan.id === Number(url.split('/').at(-1)) ? PlanRecordSchema.parse({ ...plan, ...(body as object) }) : plan);
      else throw new Error(`Unexpected story write: ${url}`);
      return new Msg<T>(true, '');
    });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof Catalogue>;
export default meta;
type Story = StoryObj<typeof meta>;
export const CatalogueView: Story = {};
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
  await userEvent.type(dialog.getByRole('textbox', { name: text('pages.policies.name') }), 'Browser plan');
  await userEvent.click(dialog.getByRole('checkbox', { name: /VLESS Reality/ }));
  await userEvent.click(dialog.getByRole('button', { name: text('pages.policies.add') }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/policies/add', expect.objectContaining({ name: 'Browser plan', inboundIds: [11], quotaBytes: 100 * 1024 ** 3, durationDays: 30 })));
  await waitFor(() => expect(within(document.body).queryByRole('dialog')).toBeNull());
} };
export const EditSave: Story = { ...EditForm, play: async () => {
  const dialog = within(await within(document.body).findByRole('dialog'));
  await userEvent.click(dialog.getByRole('button', { name: '100/25' }));
  await userEvent.click(dialog.getByRole('button', { name: text('save') }));
  await waitFor(() => expect(written).toHaveBeenCalledWith('/panel/api/policies/update/1', expect.objectContaining({
    tiers: [{ fromBytes: 0, downBps: 100_000_000, upBps: 25_000_000 }, { fromBytes: 80 * 1024 ** 3, downBps: 5_000_000, upBps: 2_000_000 }],
    startOnFirstUse: true, autoRenewDays: 30, resetCycle: 'monthly', inboundIds: [11, 12, 13],
  })));
  await waitFor(() => expect(within(document.body).queryByRole('dialog')).toBeNull());
} };
