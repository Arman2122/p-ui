import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { HttpUtil, Msg } from '@/utils';
import FleetNetwork from './FleetNetwork';

const nodeId = '33253fd2-c71a-43c6-84e5-6277e435e4d2';
const installationId = '948a18c5-a5d0-4c28-8052-d46224088bd6';
const revision = { id: 'b84cf341-f5e1-4dc6-b520-63a7d0e4b970', revision: 7, sha256: 'a'.repeat(64), createdUnixMilli: 1000 };
const specification = { version: 1, nativeNetwork: { peerIPv4: '172.31.240.2', gatewayIPv4: '172.31.240.3' }, sshHostKeys: { generateLocally: true }, inbounds: { '42': { listen: '0.0.0.0', port: 8443 } }, assetSlots: { 'ssh-certificate': 'tls-certificate', 'ssh-private-key': 'tls-private-key' } };
const nodeState = { nodeId, installationId, revision: { ...revision, revision: 3 }, defaults: revision, followDefaults: true, pending: true, configured: true, origins: { 'native-network': 'overridden', 'ssh-host-keys': 'builtin', 'inbound/42': 'overridden', 'asset-slot/ssh-certificate': 'inherited', 'asset-slot/ssh-private-key': 'inherited' }, overrides: { version: 1, followDefaults: true }, effective: specification };

function Surface({ dark }: { dark: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <ConfigProvider theme={{ token: { motion: false } }}><FleetNetwork node={{ id: nodeId, name: 'Frankfurt · edge-01' }} onClose={() => {}} /></ConfigProvider>;
}

function NetworkStory({ dark = false }: { dark?: boolean; authorization?: string; ready?: boolean }) {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } }));
  useEffect(() => () => client.clear(), [client]);
  return <QueryClientProvider client={client}><ThemeProvider><Surface dark={dark} /></ThemeProvider></QueryClientProvider>;
}

const meta = {
  title: 'Pages/Headless fleet networking', component: NetworkStory, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  render: (_, context) => <NetworkStory dark={context.globals.theme === 'dark'} />,
  beforeEach: ({ args }) => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      const value = url.endsWith('/defaults') ? { revision, specification }
        : url.endsWith('/assets') ? { items: [], nextCursor: '' }
          : url.endsWith('/options') ? [{ id: 42, remark: 'SSH WSS ingress', protocol: 'ssh', port: 8443 }] : url.endsWith(`/nodes/${nodeId}`) ? nodeState : null;
      if (value == null) throw new Error(`Unexpected networking story read: ${url}`);
      return new Msg<T>(true, '', value as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string) {
      if (url !== '/panel/api/fleet/network/preview') throw new Error('Unexpected networking story mutation');
      return new Msg<T>(true, '', {
        id: 'a7fdb5fb-381d-4d4b-ae46-fdd4db9fd51e', nodeId, installationId, nodeRevision: nodeState.revision, defaults: revision, ready: args.ready ?? true, sha256: 'e'.repeat(64), origins: nodeState.origins,
        runtimes: [{ id: 'ssh', core: 'ssh', clients: 1, sha256: 'f'.repeat(64) }], listeners: [{ runtimeId: 'ssh', inboundId: 42, address: '0.0.0.0', port: 8443, protocol: 'tcp', role: 'client' }], assets: [], diagnostics: [],
        identities: [{ clientId: '431e26d4-3c66-42f9-9556-afb427248986', credentialGeneration: 1, placementId: '3bbf825a-9d91-4bb0-ac4d-d14e16fa24b7', placementRevision: 2, runtimeId: 'ssh', inboundInstanceIds: ['6a9ffb2d-f4c0-4b92-b3f8-f1eabfd36ca4'], inboundTags: { '6a9ffb2d-f4c0-4b92-b3f8-f1eabfd36ca4': 'node-owned-ssh-target' }, state: args.authorization ?? 'authority_required' }],
      } as T);
    });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
  play: async () => {
    const page = within(document.body);
    await page.findByRole('button', { name: 'Pin current defaults' });
    const drawer = page.getByRole('dialog');
    const body = drawer.querySelector<HTMLElement>('.ant-drawer-body')!;
    expect(drawer.getBoundingClientRect().right).toBeLessThanOrEqual(window.innerWidth + 1);
    expect(body.scrollWidth).toBeLessThanOrEqual(body.clientWidth + 1);
  },
} satisfies Meta<typeof NetworkStory>;

export default meta;
type Story = StoryObj<typeof meta>;
export const Overview: Story = {};
export const OverviewDark: Story = { globals: { theme: 'dark' } };
export const Mobile: Story = { globals: { viewport: { value: 'mobile1', isRotated: false } } };
export const PendingAuthorization: Story = {
  args: { ready: false },
  play: async () => {
    const page = within(document.body);
    await userEvent.click(await page.findByRole('tab', { name: 'Review' }));
    await userEvent.click(page.getByRole('button', { name: 'Create preview' }));
    await page.findByText('Client authorization is pending');
    expect(page.getByRole('button', { name: 'Publish to node' })).toBeDisabled();
    expect(HttpUtil.post).toHaveBeenCalledTimes(1);
  },
};
export const AuthenticationDefinitions: Story = {
  args: { authorization: 'dormant' },
  play: async () => {
    const page = within(document.body);
    await userEvent.click(await page.findByRole('tab', { name: 'Review' }));
    await userEvent.click(page.getByRole('button', { name: 'Create preview' }));
    await page.findByText('Ready to publish authentication definitions');
    expect(page.getByRole('button', { name: 'Publish to node' })).toBeEnabled();
  },
};
export const ReadyForPublication: Story = {
  args: { authorization: 'active' },
  play: async () => {
    const page = within(document.body);
    await userEvent.click(await page.findByRole('tab', { name: 'Review' }));
    await userEvent.click(page.getByRole('button', { name: 'Create preview' }));
    await page.findByText('Ready for publication');
    expect(page.getByRole('button', { name: 'Publish to node' })).toBeEnabled();
  },
};
