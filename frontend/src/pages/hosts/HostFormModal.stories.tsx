import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import type { HostRecord } from '@/schemas/api/host';
import HostFormModal from './HostFormModal';

const text = (key: string) => String(i18next.t(key));
const host: HostRecord = { groupId: 'host-fixture', remark: 'CDN route', serverDescription: 'European route', hosts: ['cdn.example.net', 'backup.example.net'], inboundIds: [11], sortOrder: 3, port: 8443, security: 'tls', sni: 'tls.example.net', tags: ['EU'], alpn: ['h2'], fingerprint: 'chrome', overrideSniFromAddress: true, pinnedPeerCertSha256: ['a'.repeat(64)], verifyPeerCertByName: 'tls.example.net', hostHeader: 'ws.example.net', path: '/tunnel', vlessRoute: '443', nodeGuids: ['edge-guid'], excludeFromSubTypes: ['raw'], mihomoIpVersion: 'ipv4-prefer', mihomoX25519: true, shuffleHost: true, muxParams: '', sockoptParams: '', finalMask: '', downloadSettings: '' };
const meta = {
  title: 'Pages/Hosts/Form', component: HostFormModal, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  args: { open: true, mode: 'add', host: null, inboundOptions: [{ id: 11, tag: 'vless-tls', remark: 'TLS inbound', protocol: 'vless' }, { id: 12, tag: 'kernel-wg', remark: 'Kernel WireGuard', protocol: 'wgkernel' }], existingHosts: [host], save: fn(async () => new Msg(true, '')), onOpenChange: fn() },
  decorators: [(Story) => {
    const [client] = useState(() => {
      const cache = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
      cache.setQueryData(keys.nodes.list(), [{ id: 7, guid: 'edge-guid', name: 'European edge' }]);
      cache.setQueryData(keys.xray.config(), { xraySetting: { outbounds: [], routing: { balancers: [] } }, subscriptionOutboundTags: [] });
      return cache;
    });
    useEffect(() => () => client.clear(), [client]);
    return <QueryClientProvider client={client}><Story /></QueryClientProvider>;
  }],
  beforeEach: () => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async () => { throw new Error('Unexpected host story request'); });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async () => { throw new Error('Host story attempted a live mutation'); });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof HostFormModal>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Create: Story = {};
export const CreateDark: Story = { globals: { theme: 'dark' } };
export const CreateMobile: Story = { parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
export const Edit: Story = { args: { mode: 'edit', host } };
export const EditDark: Story = { ...Edit, globals: { theme: 'dark' } };
export const EditMobile: Story = { ...Edit, parameters: CreateMobile.parameters };
export const Security: Story = { ...Edit, play: async () => {
  const dialog = within(await within(document.body).findByRole('dialog'));
  await userEvent.click(dialog.getByRole('tab', { name: text('pages.hosts.sections.security') }));
  await waitFor(() => expect(dialog.getByLabelText(text('pages.hosts.fields.sni'))).toBeVisible());
} };
export const Transport: Story = { ...Edit, play: async () => {
  const dialog = within(await within(document.body).findByRole('dialog'));
  await userEvent.click(dialog.getByRole('tab', { name: text('pages.hosts.sections.advanced') }));
  await waitFor(() => expect(dialog.getByLabelText(text('pages.hosts.fields.path'), { exact: false, selector: 'input' })).toBeVisible());
} };
export const CreateAndSave: Story = {
  play: async ({ args }) => {
    const dialog = within(await within(document.body).findByRole('dialog'));
    await userEvent.type(dialog.getByLabelText(text('pages.hosts.fields.remark'), { exact: false }), 'New public route');
    await userEvent.click(dialog.getByLabelText(text('pages.hosts.fields.inbound')));
    expect(await within(document.body).findByRole('listbox', { name: text('pages.hosts.fields.inbound') })).toHaveAccessibleName(text('pages.hosts.fields.inbound'));
    await userEvent.click(await within(document.body).findByText('TLS inbound', { selector: '.ant-select-item-option-content' }));
    await userEvent.keyboard('{Escape}');
    const address = dialog.getByRole('combobox', { name: name => name.startsWith(text('pages.hosts.fields.address')) });
    await userEvent.click(address);
    expect(await within(document.body).findByRole('listbox', { name: text('pages.hosts.fields.address') })).toHaveAccessibleName(text('pages.hosts.fields.address'));
    await userEvent.type(address, 'new.example.net{Enter}');
    await userEvent.keyboard('{Escape}');
    await userEvent.click(dialog.getByRole('button', { name: text('save') }));
    await waitFor(() => expect(args.save).toHaveBeenCalledWith(expect.objectContaining({ remark: 'New public route', inboundIds: [11], hosts: ['new.example.net'], security: 'same', isDisabled: false })));
    expect(args.onOpenChange).toHaveBeenCalledWith(false);
  },
};
export const EditAndPreserveOverrides: Story = {
  ...Edit,
  play: async ({ args }) => {
    const dialog = within(await within(document.body).findByRole('dialog'));
    const remark = dialog.getByLabelText(text('pages.hosts.fields.remark'), { exact: false });
    await userEvent.clear(remark);
    await userEvent.type(remark, 'Renamed public route');
    await userEvent.click(dialog.getByRole('tab', { name: text('pages.hosts.sections.security') }));
    const sni = dialog.getByLabelText(text('pages.hosts.fields.sni'));
    await userEvent.clear(sni);
    await userEvent.type(sni, 'new-tls.example.net');
    await userEvent.click(dialog.getByRole('tab', { name: text('pages.hosts.sections.advanced') }));
    const path = dialog.getByLabelText(text('pages.hosts.fields.path'), { exact: false, selector: 'input' });
    await userEvent.clear(path);
    await userEvent.type(path, '/new-tunnel');
    await userEvent.click(dialog.getByRole('button', { name: text('save') }));
    await waitFor(() => expect(args.save).toHaveBeenCalledWith(expect.objectContaining({ remark: 'Renamed public route', sni: 'new-tls.example.net', path: '/new-tunnel', hosts: host.hosts, inboundIds: [11], port: 8443, sortOrder: 3, security: 'tls', alpn: ['h2'], fingerprint: 'chrome', pinnedPeerCertSha256: host.pinnedPeerCertSha256, verifyPeerCertByName: 'tls.example.net', hostHeader: 'ws.example.net', vlessRoute: '443', nodeGuids: ['edge-guid'], excludeFromSubTypes: ['raw'], mihomoIpVersion: 'ipv4-prefer', mihomoX25519: true, shuffleHost: true })));
    expect(args.onOpenChange).toHaveBeenCalledWith(false);
  },
};
