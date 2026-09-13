import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import i18next from 'i18next';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { HttpUtil, Msg } from '@/utils';
import type { NodeListProps } from './NodeList';
import NodeFleet from './NodeFleet';
import '@/styles/design-pages.css';

const text = (key: string) => String(i18next.t(key));
const nodes: NodeListProps['nodes'] = [
  { id: 1, guid: 'helsinki', name: 'Helsinki', address: '192.0.2.10', port: 443, scheme: 'https', enable: true, status: 'online', metricsAvailable: true,
    cpuPct: 14.2, memPct: 32, latencyMs: 18, clientCount: 120, inboundCount: 2, onlineCount: 8, netUp: 0, netDown: 1048576, uptimeSecs: 86400,
    xrayState: 'running', xrayVersion: '26.7.28', panelVersion: '1.0.0', servedInbounds: [{ id: 11, tag: 'vless-reality', protocol: 'vless', up: 1048576, down: 4194304 }, { id: 12, tag: 'wireguard', protocol: 'wgkernel', up: 0, down: 0 }] },
  { id: 2, guid: 'frankfurt', name: 'Frankfurt', address: '192.0.2.20', scheme: 'https', port: 443, enable: true, status: 'offline', metricsAvailable: false,
    clientCount: 45, inboundCount: 2, lastError: 'Connection timed out', servedInbounds: null },
  { id: 0, guid: 'child-oslo', parentGuid: 'helsinki', name: 'Oslo child', transitive: true, enable: true, status: 'online', metricsAvailable: false },
];

function StorySurface({ props, dark }: { props: NodeListProps; dark: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [isDark, dark, toggleTheme]);
  const currentNodes = props.nodes.map(node => node.metricsAvailable ? { ...node, lastHeartbeat: Math.floor(Date.now() / 1000) } : node);
  return <main className="p-page"><NodeFleet {...props} nodes={currentNodes} selectedIds={selectedIds} onSelectionChange={ids => { setSelectedIds(ids); props.onSelectionChange(ids); }} /></main>;
}

function NodeStory({ props, dark }: { props: NodeListProps; dark: boolean }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }));
  useEffect(() => () => client.clear(), [client]);
  return <MemoryRouter><QueryClientProvider client={client}><ThemeProvider><StorySurface props={props} dark={dark} /></ThemeProvider></QueryClientProvider></MemoryRouter>;
}

function noOverflow(canvasElement: HTMLElement) {
  const page = canvasElement.querySelector<HTMLElement>('.node-fleet')!;
  expect(page.scrollWidth).toBeLessThanOrEqual(page.clientWidth + 1);
}

const meta = {
  title: 'Pages/Nodes', component: NodeFleet, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  render: (args, context) => <NodeStory props={args} dark={context.globals.theme === 'dark'} />,
  args: { nodes, selectedIds: [], onSelectionChange: fn(), onAdd: fn(), onMtls: fn(), onEdit: fn(), onDelete: fn(), onProbe: fn(), onToggleEnable: fn(), onUpdateNode: fn(), onUpdateSelected: fn() },
  beforeEach: context => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      const match = url.match(/^\/panel\/api\/nodes\/history\/(\d+)\/(netUp|netDown|cpu|mem)\/(\d+)$/);
      if (!match || match[1] === '0') throw new Error(`Unexpected story request: ${url}`);
      if (context.parameters.historyFailure) return new Msg<T>(false, 'History unavailable');
      const end = Math.floor(Date.now() / 1000 / Number(match[3])) * Number(match[3]);
      const points = match[1] === '1' ? Array.from({ length: 30 }, (_, index) => ({ t: end - (29 - index) * Number(match[3]), v: (index % 7 + 1) * (match[2] === 'netDown' ? 196608 : 32768) })) : [];
      return new Msg(true, '', points as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async () => { throw new Error('Node story attempted a live mutation'); });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof NodeFleet>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Fleet: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvasElement.querySelector('.node-throughput .uplot')).not.toBeNull());
    expect(canvasElement.querySelectorAll('.node-resource')).toHaveLength(9);
    expect(canvas.getAllByText(text('nodesFidelity.disk'))).toHaveLength(3);
    await waitFor(() => expect(canvas.getByText('vless-reality · vless')).toBeVisible());
    expect(canvas.getByRole('radio', { name: text('design.cardView') })).toBeChecked();
    expect(canvas.queryByRole('table')).toBeNull();
    expect(canvas.queryByRole('dialog')).toBeNull();
    noOverflow(canvasElement);
  },
};

export const FleetDark: Story = { ...Fleet, globals: { theme: 'dark' } };

export const MobileFleet: Story = {
  ...Fleet,
  args: { isMobile: true },
  parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } },
};

export const MobileTable: Story = {
  args: { isMobile: true }, parameters: MobileFleet.parameters,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const tableChoice = canvas.getByRole('radio', { name: text('design.tableView') }).closest('label')!;
    await waitFor(() => expect(tableChoice).toBeVisible());
    await userEvent.click(tableChoice);
    await waitFor(() => expect(canvas.getByRole('table')).toBeVisible());
    expect(canvas.getByText('Oslo child')).toBeVisible();
    await userEvent.click(canvas.getByRole('radio', { name: text('design.cardView') }).closest('label')!);
    expect(canvas.queryByRole('table')).toBeNull();
    noOverflow(canvasElement);
  },
};

export const HistoryUnavailable: Story = {
  parameters: { historyFailure: true },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvas.getAllByText(text('telemetry.historyUnavailable')).length).toBeGreaterThan(0));
    expect(canvasElement.querySelector('.uplot')).toBeNull();
    expect(canvas.getAllByText(text('nodesFidelity.notReported')).length).toBeGreaterThan(0);
    noOverflow(canvasElement);
  },
};

export const DetailHistory: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: 'Helsinki' }));
    const dialog = await within(document.body).findByRole('dialog');
    await waitFor(() => expect(dialog.querySelectorAll('.uplot')).toHaveLength(4));
    await waitFor(() => expect(within(dialog).getByText(text('telemetry.hour'))).toBeVisible());
  },
};

export const CardActions: Story = {
  play: async ({ canvasElement, args }) => {
    const canvas = within(canvasElement);
    const card = within(canvas.getByRole('button', { name: 'Helsinki' }).closest('section')!);
    await userEvent.click(card.getByRole('button', { name: text('check') }));
    expect(args.onProbe).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }));
    await userEvent.click(card.getByRole('button', { name: text('more') }));
    await userEvent.click(await within(document.body).findByRole('menuitem', { name: text('edit') }));
    expect(args.onEdit).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }));
    await userEvent.click(card.getByRole('button', { name: text('more') }));
    await userEvent.click(await within(document.body).findByRole('menuitem', { name: text('pages.clients.disable') }));
    expect(args.onToggleEnable).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }), false);
    await userEvent.click(card.getByRole('checkbox'));
    await userEvent.click(canvas.getByRole('button', { name: String(i18next.t('pages.nodes.updateSelected', { count: 1 })) }));
    expect(args.onUpdateSelected).toHaveBeenCalledOnce();
    const child = within(canvas.getByRole('button', { name: 'Oslo child' }).closest('section')!);
    expect(child.queryByRole('button', { name: text('more') })).toBeNull();
    expect(child.queryByRole('checkbox')).toBeNull();
  },
};
