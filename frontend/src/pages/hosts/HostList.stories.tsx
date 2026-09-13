import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import i18next from 'i18next';
import { ClipboardManager } from '@/utils';
import type { HostRecord } from '@/schemas/api/host';
import HostList from './HostList';

const text = (key: string) => String(i18next.t(key));
const hosts: HostRecord[] = [
  { groupId: 'direct', remark: 'Direct endpoint', hosts: ['edge.example.net'], inboundIds: [11], port: 443, security: 'reality', sni: 'www.example.net', sortOrder: 0, isDisabled: false },
  { groupId: 'cdn', remark: 'CDN endpoint', hosts: ['cdn.example.net', 'backup.example.net'], inboundIds: [12, 13], port: 8443, security: 'tls', sni: 'cdn.example.net', sortOrder: 1, isDisabled: false, tags: ['EU'], serverDescription: 'European route' },
  { groupId: 'inherit', remark: 'Inherited endpoint', hosts: [], inboundIds: [11], port: 0, security: 'same', sortOrder: 2, isDisabled: true },
];
const meta = {
  title: 'Pages/Hosts', component: HostList, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  args: { hosts, inboundOptions: [{ id: 11, tag: 'vless-reality', protocol: 'vless' }, { id: 12, tag: 'trojan-tls', protocol: 'trojan' }, { id: 13, tag: 'vless-ws', protocol: 'vless' }], selectedGroupIds: [], onSelectionChange: fn(), onAdd: fn(), onEdit: fn(), onDelete: fn(), onToggleEnable: fn(), onMove: fn(), onBulkEnable: fn(), onBulkDelete: fn() },
  render: function HostStory(args) {
    const [selected, setSelected] = useState<string[]>([]);
    return <main className="p-page"><HostList {...args} selectedGroupIds={selected} onSelectionChange={ids => { setSelected(ids); args.onSelectionChange(ids); }} /></main>;
  },
  beforeEach: () => {
    const clipboard = spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    return () => clipboard.mockRestore();
  },
} satisfies Meta<typeof HostList>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Hosts: Story = {};
export const HostsDark: Story = { globals: { theme: 'dark' } };
export const HostsMobile: Story = { args: { isMobile: true }, parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
export const Actions: Story = {
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    const row = within(canvas.getByText('CDN endpoint').closest('tr')!);
    await userEvent.click(row.getByRole('button', { name: text('edit') }));
    expect(args.onEdit).toHaveBeenCalledWith(hosts[1]);
    await userEvent.click(row.getByRole('button', { name: text('designParityHosts.copyAddress') }));
    expect(ClipboardManager.copyText).toHaveBeenCalledWith('cdn.example.net:8443\nbackup.example.net:8443');
    await userEvent.click(row.getByRole('switch'));
    expect(args.onToggleEnable).toHaveBeenCalledWith(hosts[1], false);
    await userEvent.click(row.getByRole('checkbox'));
    await userEvent.click(canvas.getByRole('button', { name: text('pages.hosts.bulkEnable') }));
    expect(args.onBulkEnable).toHaveBeenCalledWith(true);
    await userEvent.click(row.getByRole('button', { name: text('delete') }));
    expect(args.onDelete).toHaveBeenCalledWith(hosts[1]);
  },
};
export const FilteredReorder: Story = {
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    await userEvent.type(canvas.getByRole('searchbox', { name: text('search') }), 'CDN endpoint');
    await waitFor(() => expect(canvas.queryByText('Direct endpoint')).toBeNull());
    await userEvent.click(canvas.getByRole('button', { name: text('more') }));
    const up = await within(document.body).findByRole('menuitem', { name: text('pages.hosts.moveUp') });
    expect(up).not.toHaveAttribute('aria-disabled', 'true');
    await userEvent.click(up);
    expect(args.onMove).toHaveBeenCalledWith(hosts[1], 'up');
  },
};
