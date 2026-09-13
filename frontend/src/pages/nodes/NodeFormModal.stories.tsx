import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import type { NodeRecord, ProbeResult } from '@/schemas/node';
import NodeFormModal from './NodeFormModal';

const text = (key: string) => String(i18next.t(key));
const storedNode: NodeRecord = { id: 7, guid: 'edge-fixture', name: 'Amsterdam edge', remark: 'EU edge', scheme: 'https', address: '192.0.2.7', port: 2053, basePath: '/private/', hasApiToken: true, enable: true, allowPrivateAddress: true, tlsVerifyMode: 'pin', pinnedCertSha256: 'a'.repeat(64), inboundSyncMode: 'selected', inboundTags: ['vless-reality'], outboundTag: 'edge-egress' };

const meta = {
  title: 'Pages/Nodes/Form', component: NodeFormModal, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  args: { open: true, mode: 'add', node: null, testConnection: fn(async (): Promise<Msg<ProbeResult>> => new Msg(true, '', { status: 'online', latencyMs: 12, xrayVersion: '26.7.28' })), fetchFingerprint: fn(async () => new Msg(true, '', 'b'.repeat(64))), fetchInbounds: fn(async () => new Msg(true, '', [{ tag: 'vless-reality', protocol: 'vless', port: 443 }])), save: fn(async () => new Msg(true, '')), onOpenChange: fn() },
  decorators: [(Story) => {
    const [client] = useState(() => {
      const cache = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
      cache.setQueryData(keys.xray.config(), { xraySetting: { outbounds: [{ tag: 'edge-egress', protocol: 'freedom' }], routing: { balancers: [{ tag: 'edge-balancer' }] } }, subscriptionOutboundTags: [] });
      return cache;
    });
    useEffect(() => () => client.clear(), [client]);
    return <QueryClientProvider client={client}><Story /></QueryClientProvider>;
  }],
  beforeEach: () => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async () => { throw new Error('Unexpected form story request'); });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async () => { throw new Error('Form story attempted a live mutation'); });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof NodeFormModal>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Create: Story = {};
export const CreateDark: Story = { globals: { theme: 'dark' } };
export const CreateMobile: Story = { parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
export const Edit: Story = { args: { mode: 'edit', node: storedNode } };
export const EditDark: Story = { ...Edit, globals: { theme: 'dark' } };
export const EditMobile: Story = { ...Edit, parameters: CreateMobile.parameters };

async function fillRequired() {
  const dialog = within(await within(document.body).findByRole('dialog'));
  await userEvent.type(dialog.getByLabelText(text('pages.nodes.name')), 'New edge');
  await userEvent.type(dialog.getByLabelText(text('pages.nodes.address')), '192.0.2.10');
  await userEvent.type(dialog.getByLabelText(text('pages.nodes.apiToken'), { exact: false }), 'story-token');
  return dialog;
}
export const CreateAndSave: Story = {
  play: async ({ args }) => {
    const dialog = await fillRequired();
    await userEvent.click(dialog.getByRole('button', { name: text('designParityNodes.connect') }));
    await waitFor(() => expect(args.save).toHaveBeenCalledWith(expect.objectContaining({ name: 'New edge', address: '192.0.2.10', port: 2053, scheme: 'https', apiToken: 'story-token', tlsVerifyMode: 'verify', inboundSyncMode: 'all', inboundTags: [] })));
    expect(args.testConnection.mock.invocationCallOrder[0]).toBeLessThan(args.save.mock.invocationCallOrder[0]);
    expect(args.onOpenChange).toHaveBeenCalledWith(false);
  },
};
export const EditStoredToken: Story = {
  ...Edit,
  play: async ({ args }) => {
    const dialog = within(await within(document.body).findByRole('dialog'));
    const name = dialog.getByLabelText(text('pages.nodes.name'));
    await userEvent.clear(name);
    await userEvent.type(name, 'Renamed edge');
    await userEvent.click(dialog.getByRole('button', { name: text('save') }));
    await waitFor(() => expect(args.save).toHaveBeenCalledWith(expect.objectContaining({ id: 7, name: 'Renamed edge', basePath: '/private/', tlsVerifyMode: 'pin', pinnedCertSha256: 'a'.repeat(64), allowPrivateAddress: true, inboundSyncMode: 'selected', inboundTags: ['vless-reality'], outboundTag: 'edge-egress' })));
    expect(args.save).not.toHaveBeenCalledWith(expect.objectContaining({ apiToken: expect.anything() }));
  },
};
export const EnrollMtls: Story = {
  play: async ({ args }) => {
    const dialog = within(await within(document.body).findByRole('dialog'));
    await userEvent.type(dialog.getByLabelText(text('pages.nodes.name')), 'mTLS edge');
    await userEvent.type(dialog.getByLabelText(text('pages.nodes.address')), '192.0.2.11');
    await userEvent.click(dialog.getByLabelText(text('pages.nodes.tlsVerifyMode'), { exact: false }));
    expect(await within(document.body).findByRole('listbox', { name: text('pages.nodes.tlsVerifyMode') })).toHaveAccessibleName(text('pages.nodes.tlsVerifyMode'));
    await userEvent.click(await within(document.body).findByText(text('pages.nodes.tlsMtls'), { selector: '.ant-select-item-option-content' }));
    await userEvent.click(dialog.getByRole('button', { name: text('designParityNodes.connect') }));
    await waitFor(() => expect(args.save).toHaveBeenCalledWith(expect.objectContaining({ tlsVerifyMode: 'mtls', name: 'mTLS edge' })));
    expect(args.save).not.toHaveBeenCalledWith(expect.objectContaining({ apiToken: expect.anything() }));
  },
};
export const FailedConnection: Story = {
  args: { testConnection: fn(async (): Promise<Msg<ProbeResult>> => new Msg(true, '', { status: 'offline', error: 'Story connection refused' })) },
  play: async ({ args }) => {
    const dialog = await fillRequired();
    await userEvent.click(dialog.getByRole('button', { name: text('designParityNodes.connect') }));
    await waitFor(() => expect(dialog.getByText('Story connection refused')).toBeVisible());
    expect(args.save).not.toHaveBeenCalled();
    expect(args.onOpenChange).not.toHaveBeenCalled();
  },
};
