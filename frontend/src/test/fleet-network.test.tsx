import { afterEach, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '@/hooks/useTheme';
import FleetNetwork from '@/pages/nodes/FleetNetwork';
import FleetNetworkAssets from '@/pages/nodes/FleetNetworkAssets';
import FleetNetworkPreviewPanel from '@/pages/nodes/FleetNetworkPreview';
import { FleetNodeNetworkStateViewSchema } from '@/generated/zod';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

const nodeID = '33253fd2-c71a-43c6-84e5-6277e435e4d2';
const installationID = '948a18c5-a5d0-4c28-8052-d46224088bd6';
const assetID = '17f763c4-6530-41f7-8a88-534560d4397e';
const revision = { id: 'b84cf341-f5e1-4dc6-b520-63a7d0e4b970', revision: 7, sha256: 'a'.repeat(64), createdUnixMilli: 1000 };
const spec = { version: 1, inbounds: { '42': { listen: '127.0.0.1', port: 8443, settings: { proto: 'tcp' } } }, nativeNetwork: { peerIPv4: '172.31.240.2', gatewayIPv4: '172.31.240.3' }, sshHostKeys: { generateLocally: true }, assetSlots: { certificate: 'tls-certificate' } };
const state = FleetNodeNetworkStateViewSchema.parse({ nodeId: nodeID, installationId: installationID, revision: { ...revision, revision: 3 }, defaults: revision, followDefaults: true, pending: true, configured: true, origins: { 'native-network': 'overridden', 'ssh-host-keys': 'inherited', 'inbound/42': 'overridden' }, overrides: { version: 1, followDefaults: true, assetBindings: { certificate: { assetId: assetID, revision: 1 } } }, effective: spec });
const metadata = { assetId: assetID, revision: 2, nodeId: nodeID, installationId: installationID, kind: 'tls-certificate', sha256: 'd'.repeat(64), size: 128, createdUnixMilli: 2000 };
const identity = { clientId: '431e26d4-3c66-42f9-9556-afb427248986', credentialGeneration: 1, placementId: '3bbf825a-9d91-4bb0-ac4d-d14e16fa24b7', placementRevision: 2, runtimeId: 'ssh', inboundInstanceIds: ['6a9ffb2d-f4c0-4b92-b3f8-f1eabfd36ca4'], inboundTags: { '6a9ffb2d-f4c0-4b92-b3f8-f1eabfd36ca4': 'node-owned-ssh-target' }, state: 'active', termId: 'c23c5f97-f9db-4d4d-9afd-83a0f4c5aff8' };
const preview = { id: 'a7fdb5fb-381d-4d4b-ae46-fdd4db9fd51e', nodeId: nodeID, installationId: installationID, nodeRevision: state.revision, defaults: revision, ready: true, sha256: 'e'.repeat(64), runtimes: [{ id: 'ssh', core: 'ssh', clients: 1, sha256: 'f'.repeat(64) }], listeners: [{ runtimeId: 'ssh', inboundId: 42, address: '0.0.0.0', port: 8443, protocol: 'tcp', role: 'client' }], assets: [], identities: [identity], diagnostics: [], origins: {} };

afterEach(() => { vi.restoreAllMocks(); });

function responses() {
  vi.mocked(HttpUtil.get).mockImplementation(async <T,>(url: string) => new Msg<T>(true, '', (
    url.endsWith('/defaults') ? { revision, specification: spec }
      : url.endsWith('/assets') ? { items: [metadata], nextCursor: '' }
        : url.endsWith('/options') ? [{ id: 42, remark: 'Existing VPN', port: 8443 }] : state
  ) as T));
}

async function nativeRow() {
  await waitFor(() => expect(document.querySelector('[data-row-key="native-network"]')).not.toBeNull());
  return document.querySelector('[data-row-key="native-network"]') as HTMLElement;
}

function rowButton(row: HTMLElement, name: string) {
  const button = Array.from(row.querySelectorAll('button')).find(item => item.textContent === name);
  expect(button).toBeDefined();
  return button!;
}

it('pins the viewed defaults with installation and revision fences and no unrelated operations', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', state));
  renderWithProviders(<FleetNetwork node={{ id: nodeID, name: 'fra-1' }} onClose={() => {}} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Pin current defaults' }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith(`/panel/api/fleet/network/nodes/${nodeID}`, {
    nodeId: nodeID, installationId: installationID, expectedRevision: 3, followDefaults: false, pinnedDefaultsId: revision.id, operations: [],
  }, { silent: true, headers: { 'Content-Type': 'application/json' } }));
});

it('resets a node override through an explicit reset and leaves inherited values intact', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', state));
  renderWithProviders(<FleetNetwork node={{ id: nodeID, name: 'fra-1' }} onClose={() => {}} />);
  const row = await nativeRow();
  fireEvent.click(rowButton(row, 'Reset to inherited'));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith(`/panel/api/fleet/network/nodes/${nodeID}`, {
    nodeId: nodeID, installationId: installationID, expectedRevision: 3, operations: [{ section: 'native-network', operation: 'reset' }],
  }, expect.objectContaining({ headers: { 'Content-Type': 'application/json' } })));
});

it('uses the edited defaults snapshot and rejects concurrent-revision errors without discarding the form', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(false, 'fleet network source revision changed'));
  renderWithProviders(<FleetNetwork onClose={() => {}} />);
  const row = await nativeRow();
  fireEvent.click(rowButton(row, 'Edit'));
  fireEvent.change(screen.getByLabelText('Native routing peer IPv4'), { target: { value: '172.31.241.2' } });
  fireEvent.change(screen.getByLabelText('Native routing gateway IPv4'), { target: { value: '172.31.241.3' } });
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await screen.findByText('fleet network source revision changed');
  expect(screen.getByLabelText('Native routing peer IPv4')).toHaveProperty('value', '172.31.241.2');
  expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/fleet/network/defaults', { expectedRevision: 7, specification: { ...spec, nativeNetwork: { peerIPv4: '172.31.241.2', gatewayIPv4: '172.31.241.3' } } }, expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }));
});

it('does not retarget an open node editor after a new installation appears in the cache', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(false, 'fleet network source revision changed'));
  const client = makeTestQueryClient();
  renderWithProviders(<FleetNetwork node={{ id: nodeID, name: 'fra-1' }} onClose={() => {}} />, { queryClient: client });
  const row = await nativeRow();
  fireEvent.click(rowButton(row, 'Edit'));
  await act(async () => { client.setQueryData(['fleet', 'network', 'node', nodeID], { ...state, installationId: 'c96a7449-8712-43d1-83a3-b82bc519c8e2' }); });
  await screen.findByText('c96a7449-8712-43d1-83a3-b82bc519c8e2');
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith(`/panel/api/fleet/network/nodes/${nodeID}`, expect.objectContaining({ installationId: installationID, expectedRevision: 3 }), expect.anything()));
});

it('uploads a new asset revision without displaying bytes or changing existing bindings', async () => {
  responses();
  const secret = 'private-certificate-material-never-display';
  const onSave = vi.fn();
  let uploaded: unknown;
  vi.mocked(HttpUtil.post).mockImplementation(async <T,>(_url: string, data?: unknown) => {
    uploaded = structuredClone(data);
    return new Msg<T>(true, '', { ...metadata, revision: 3 } as T);
  });
  renderWithProviders(<FleetNetworkAssets state={state} onSave={onSave} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Upload new version' }));
  const file = new File([secret], 'node.pem');
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new TextEncoder().encode(secret).buffer });
  fireEvent.change(screen.getByLabelText('File'), { target: { files: [file] } });
  expect(screen.queryByText(secret)).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalled());
  const request = vi.mocked(HttpUtil.post).mock.calls[0];
  expect(request[0]).toBe(`/panel/api/fleet/network/nodes/${nodeID}/assets`);
  expect(request[1]).toEqual(expect.objectContaining({ assetId: assetID, installationId: installationID, nodeId: nodeID, expectedRevision: 2, kind: 'tls-certificate' }));
  expect(uploaded).toEqual(expect.objectContaining({ data: btoa(secret) }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(screen.queryByText(secret)).toBeNull();
  expect(screen.queryByText(btoa(secret))).toBeNull();
  expect(request[1]).toEqual(expect.objectContaining({ data: '' }));
  expect(onSave).not.toHaveBeenCalled();
});

it('keeps asset upload bound to the installation selected when its form opened', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(false, 'fleet network source revision changed'));
  const client = makeTestQueryClient();
  const onSave = vi.fn();
  const view = renderWithProviders(<FleetNetworkAssets state={state} onSave={onSave} />, { queryClient: client });
  fireEvent.click(await screen.findByRole('button', { name: 'Upload new version' }));
  view.rerender(<QueryClientProvider client={client}><ThemeProvider><FleetNetworkAssets state={{ ...state, installationId: 'c96a7449-8712-43d1-83a3-b82bc519c8e2' }} onSave={onSave} /></ThemeProvider></QueryClientProvider>);
  const file = new File(['certificate'], 'node.pem');
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new TextEncoder().encode('certificate').buffer });
  fireEvent.change(screen.getByLabelText('File'), { target: { files: [file] } });
  fireEvent.click(screen.getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(HttpUtil.post).toHaveBeenCalledWith(`/panel/api/fleet/network/nodes/${nodeID}/assets`, expect.objectContaining({ installationId: installationID, expectedRevision: 2 }), expect.anything()));
});

it('shows blocking preview requirements and never enables publication for an unready result', async () => {
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', { ...preview, ready: false, diagnostics: [{ code: 'NODE_ASSET_REQUIRED', assetSlot: 'certificate' }] }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('NODE_ASSET_REQUIRED');
  const publish = screen.getByRole('button', { name: 'Publish to node' });
  expect(publish).toHaveProperty('disabled', true);
  fireEvent.click(publish);
  expect(HttpUtil.post).toHaveBeenCalledTimes(1);
  expect(HttpUtil.post).toHaveBeenCalledWith('/panel/api/fleet/network/preview', { nodeId: nodeID, installationId: installationID, expectedRevision: 3, expectedDefaultsId: revision.id }, expect.anything());
});

it('reuses the publication operation id after an ambiguous failure and sends only the reviewed compilation', async () => {
  const published = vi.fn().mockResolvedValue(undefined);
  vi.mocked(HttpUtil.post)
    .mockResolvedValueOnce(new Msg(true, '', preview))
    .mockResolvedValueOnce(new Msg(false, 'publication response was lost'))
    .mockResolvedValueOnce(new Msg(true, '', { nodeId: nodeID, compilationId: preview.id, deploymentRevision: 12, replayed: true }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={published} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('Ready for publication');
  fireEvent.click(screen.getByRole('button', { name: 'Publish to node' }));
  await screen.findByText('publication response was lost');
  fireEvent.click(screen.getByRole('button', { name: 'Publish to node' }));
  await waitFor(() => expect(published).toHaveBeenCalledOnce());
  const calls = vi.mocked(HttpUtil.post).mock.calls.filter(([url]) => url.endsWith('/publish'));
  expect(calls).toHaveLength(2);
  expect(calls[0][1]).toEqual(calls[1][1]);
  expect(calls[0][1]).toEqual({ compilationId: preview.id, operationId: expect.stringMatching(/^[0-9a-f-]{36}$/) });
});

it('refuses a ready preview from a different pinned defaults revision', async () => {
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', { ...preview, defaults: { ...revision, id: '18beec56-c336-490e-9723-9a1a9e384611' } }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('Reload the current revision before saving.');
  expect(screen.getByRole('button', { name: 'Publish to node' })).toHaveProperty('disabled', true);
});

it.each(['cutover_required', 'inactive', 'reconciliation_required', 'unrecognized'])('blocks publication of a compiled graph while a client is %s', async authorization => {
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', { ...preview, identities: [{ ...identity, state: authorization }] }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('Client authorization is pending');
  expect(screen.queryByText('Ready for publication')).toBeNull();
  expect(screen.getByRole('button', { name: 'Publish to node' })).toHaveProperty('disabled', true);
  expect(HttpUtil.post).toHaveBeenCalledTimes(1);
});

it.each(['authority_required', 'dormant'])('allows explicitly qualified %s authentication definitions without claiming forwarding readiness', async authorization => {
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', { ...preview, identities: [{ ...identity, state: authorization, termId: undefined }] }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('Ready to publish authentication definitions');
  expect(screen.queryByText('Ready for publication')).toBeNull();
  expect(screen.getByRole('button', { name: 'Publish to node' })).toHaveProperty('disabled', false);
  expect(HttpUtil.post).toHaveBeenCalledTimes(1);
});

it.each([
  { ready: false, inboundTags: identity.inboundTags },
  { ready: true, inboundTags: {} },
  { ready: true, inboundTags: { 'foreign-instance': 'node-owned-ssh-target' } },
])('blocks auth-only publication without ready capability and complete pinned inbound scope: %j', async value => {
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', { ...preview, ready: value.ready, identities: [{ ...identity, state: 'authority_required', inboundTags: value.inboundTags }] }));
  renderWithProviders(<FleetNetworkPreviewPanel state={state} onPublished={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Create preview' }));
  await screen.findByText('Client authorization is pending');
  expect(screen.getByRole('button', { name: 'Publish to node' })).toHaveProperty('disabled', true);
});

it('invalidates a displayed preview when the selected node revision changes', async () => {
  responses();
  vi.mocked(HttpUtil.post).mockResolvedValue(new Msg(true, '', preview));
  const client = makeTestQueryClient();
  renderWithProviders(<FleetNetwork node={{ id: nodeID, name: 'fra-1' }} onClose={() => {}} />, { queryClient: client });
  await nativeRow();
  const tab = Array.from(document.querySelectorAll('[role="tab"]')).find(item => item.textContent === 'Review');
  expect(tab).toBeDefined();
  fireEvent.click(tab!);
  fireEvent.click(rowButton(document.body, 'Create preview'));
  await screen.findByText('Ready for publication');
  await act(async () => { client.setQueryData(['fleet', 'network', 'node', nodeID], { ...state, revision: { ...revision, id: '18beec56-c336-490e-9723-9a1a9e384611', revision: 4 } }); });
  await waitFor(() => expect(screen.queryByText('Ready for publication')).toBeNull());
  expect(rowButton(document.body, 'Publish to node')).toHaveProperty('disabled', true);
});
