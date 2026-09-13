import { fireEvent, screen, waitFor } from '@testing-library/react';
import { QueryClient } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { ClipboardManager, Msg } from '@/utils';
import type { HostRecord } from '@/schemas/api/host';
import type { NodeRecord, ProbeResult } from '@/schemas/node';
import HostList from '@/pages/hosts/HostList';
import HostFormModal from '@/pages/hosts/HostFormModal';
import NodeFormModal from '@/pages/nodes/NodeFormModal';
import { chooseSelectOption, renderWithProviders } from './test-utils';

const text = (key: string) => String(i18next.t(key));
const clients: QueryClient[] = [];
function cache() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  client.setQueryData(keys.nodes.list(), [{ id: 7, guid: 'edge-guid', name: 'European edge' }]);
  client.setQueryData(keys.xray.config(), { xraySetting: { outbounds: [{ tag: 'egress', protocol: 'freedom' }], routing: { balancers: [] } }, subscriptionOutboundTags: [] });
  clients.push(client);
  return client;
}
afterEach(() => { clients.splice(0).forEach(client => client.clear()); vi.restoreAllMocks(); });
const hosts: HostRecord[] = [
  { groupId: 'first', remark: 'First', hosts: ['first.example.net'], inboundIds: [11], sortOrder: 0 },
  { groupId: 'middle', remark: 'Middle', hosts: ['cdn.example.net', 'backup.example.net'], inboundIds: [11], port: 8443, sortOrder: 1 },
  { groupId: 'last', remark: 'Last', hosts: [], inboundIds: [11], sortOrder: 2 },
];
function hostList() {
  const actions = { onSelectionChange: vi.fn(), onAdd: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onToggleEnable: vi.fn(), onMove: vi.fn(), onBulkEnable: vi.fn(), onBulkDelete: vi.fn() };
  renderWithProviders(<HostList hosts={hosts} inboundOptions={[]} selectedGroupIds={[]} {...actions} />);
  return actions;
}

describe('Nodes and Hosts design parity regressions', () => {
  it('reorders a filtered host using its saved list position', async () => {
    const actions = hostList();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'Middle' } });
    fireEvent.click(screen.getByRole('button', { name: text('more') }));
    const moveUp = await screen.findByRole('menuitem', { name: text('pages.hosts.moveUp') });
    expect(moveUp.getAttribute('aria-disabled')).not.toBe('true');
    fireEvent.click(moveUp);
    expect(actions.onMove).toHaveBeenCalledWith(hosts[1], 'up');
  });

  it('copies every explicit public address with its configured port', async () => {
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    hostList();
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'Middle' } });
    fireEvent.click(screen.getByRole('button', { name: text('designParityHosts.copyAddress') }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('cdn.example.net:8443\nbackup.example.net:8443'));
  });

  it('edits a host without dropping transport, TLS, subscription, or stored legacy values', async () => {
    const host: HostRecord = { ...hosts[1], security: 'tls', sni: 'tls.example.net', hostHeader: 'ws.example.net', path: '/tunnel', tags: ['EU'], alpn: ['h2'], fingerprint: 'chrome', overrideSniFromAddress: true, pinnedPeerCertSha256: ['a'.repeat(64)], verifyPeerCertByName: 'tls.example.net', vlessRoute: '443', excludeFromSubTypes: ['raw'], nodeGuids: ['edge-guid'], mihomoIpVersion: 'ipv4-prefer', mihomoX25519: true, shuffleHost: true,
      muxParams: '{"enabled":true,"concurrency":4,"xudpConcurrency":8,"xudpProxyUDP443":"reject"}', sockoptParams: '{"tcpFastOpen":true,"tcpKeepAliveInterval":30}', finalMask: '', downloadSettings: '' };
    const save = vi.fn(async () => new Msg(true, ''));
    const close = vi.fn();
    renderWithProviders(<HostFormModal open mode="edit" host={host} existingHosts={hosts} inboundOptions={[{ id: 11, tag: 'vless-tls', protocol: 'vless' }]} save={save} onOpenChange={close} />, { queryClient: cache() });
    fireEvent.change(screen.getByLabelText(text('pages.hosts.fields.remark'), { exact: false }), { target: { value: 'Edited host' } });
    fireEvent.click(screen.getByRole('tab', { name: text('pages.hosts.sections.security') }));
    fireEvent.change(screen.getByLabelText(text('pages.hosts.fields.sni')), { target: { value: 'new-tls.example.net' } });
    fireEvent.click(screen.getByRole('tab', { name: text('pages.hosts.sections.advanced') }));
    fireEvent.change(screen.getByLabelText(text('pages.hosts.fields.path'), { exact: false, selector: 'input' }), { target: { value: '/new-tunnel' } });
    fireEvent.click(screen.getByRole('button', { name: text('save') }));
    await waitFor(() => expect(save).toHaveBeenCalledOnce());
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ remark: 'Edited host', sni: 'new-tls.example.net', path: '/new-tunnel', hostHeader: host.hostHeader, hosts: host.hosts, inboundIds: [11], port: 8443, sortOrder: 1, security: 'tls', alpn: ['h2'], fingerprint: 'chrome', pinnedPeerCertSha256: host.pinnedPeerCertSha256, verifyPeerCertByName: host.verifyPeerCertByName, vlessRoute: '443', excludeFromSubTypes: ['raw'], nodeGuids: ['edge-guid'], mihomoIpVersion: 'ipv4-prefer', mihomoX25519: true, shuffleHost: true }));
    const payload = save.mock.calls[0] as unknown as [Record<string, unknown>];
    expect(JSON.parse(String(payload[0].muxParams))).toMatchObject({ enabled: true, concurrency: 4 });
    expect(JSON.parse(String(payload[0].sockoptParams))).toMatchObject({ tcpFastOpen: true, tcpKeepAliveInterval: 30 });
    expect(close).toHaveBeenCalledWith(false);
  });

  it('preserves a node stored token, pin, private address and selected inbound routing on edit', async () => {
    const node: NodeRecord = { id: 7, name: 'Edge', address: '192.0.2.7', scheme: 'https', port: 2053, basePath: '/private/', hasApiToken: true, tlsVerifyMode: 'pin', pinnedCertSha256: 'a'.repeat(64), inboundSyncMode: 'selected', inboundTags: ['vless-tls'], outboundTag: 'egress', allowPrivateAddress: true, enable: true };
    const actions = nodeForm(node);
    fireEvent.change(screen.getByLabelText(text('pages.nodes.name')), { target: { value: 'Renamed edge' } });
    fireEvent.click(screen.getByRole('button', { name: text('save') }));
    await waitFor(() => expect(actions.save).toHaveBeenCalledOnce());
    expect(actions.save).toHaveBeenCalledWith(expect.objectContaining({ id: 7, name: 'Renamed edge', basePath: '/private/', allowPrivateAddress: true, tlsVerifyMode: 'pin', pinnedCertSha256: 'a'.repeat(64), inboundSyncMode: 'selected', inboundTags: ['vless-tls'], outboundTag: 'egress' }));
    expect(actions.save.mock.calls[0][0]).not.toHaveProperty('apiToken');
    expect(actions.testConnection.mock.invocationCallOrder[0]).toBeLessThan(actions.save.mock.invocationCallOrder[0]);
  });

  it('allows mTLS enrollment without inventing a token', async () => {
    const actions = nodeForm(null);
    fireEvent.change(screen.getByLabelText(text('pages.nodes.name')), { target: { value: 'mTLS edge' } });
    fireEvent.change(screen.getByLabelText(text('pages.nodes.address')), { target: { value: '192.0.2.10' } });
    chooseSelectOption(screen.getByLabelText(text('pages.nodes.tlsVerifyMode'), { exact: false }).id, text('pages.nodes.tlsMtls'));
    fireEvent.click(screen.getByRole('button', { name: text('designParityNodes.connect') }));
    await waitFor(() => expect(actions.save).toHaveBeenCalledOnce());
    expect(actions.save.mock.calls[0][0]).toMatchObject({ tlsVerifyMode: 'mtls', name: 'mTLS edge' });
    expect(actions.save.mock.calls[0][0]).not.toHaveProperty('apiToken');
  });

  it('retains the form and refuses to save a failed connection', async () => {
    const actions = nodeForm({ id: 7, name: 'Edge', address: '192.0.2.7', hasApiToken: true }, { status: 'offline', error: 'Connection refused' });
    fireEvent.click(screen.getByRole('button', { name: text('save') }));
    await screen.findByText('Connection refused');
    expect(actions.save).not.toHaveBeenCalled();
    expect(actions.onOpenChange).not.toHaveBeenCalled();
  });
});

function nodeForm(node: NodeRecord | null, result: ProbeResult = { status: 'online', latencyMs: 12 }) {
  const actions = { testConnection: vi.fn(async (_payload: Partial<NodeRecord>) => new Msg(true, '', result)), fetchFingerprint: vi.fn(async () => new Msg(true, '', 'b'.repeat(64))), fetchInbounds: vi.fn(async () => new Msg(true, '', [{ tag: 'vless-tls' }])), save: vi.fn(async (_payload: Partial<NodeRecord>) => new Msg(true, '')), onOpenChange: vi.fn() };
  renderWithProviders(<NodeFormModal open mode={node ? 'edit' : 'add'} node={node} {...actions} />, { queryClient: cache() });
  return actions;
}
