import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { FleetClientCreationStatus } from '@/pages/clients/FleetClientCreationStatus';
import { useFleetClientCreation } from '@/pages/clients/useFleetClientCreation';
import { creationAdmin, creationOperationOne, creationOperationTwo, creationRequest, creationResult } from '@/pages/clients/FleetClientCreation.fixtures';
import { HttpUtil, Msg, RandomUtil } from '@/utils';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

const path = '/panel/api/fleet/clients';
const key = 'fleet-client-creation:1';
afterEach(() => { sessionStorage.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function Harness({ request = creationRequest, accepted = () => {} }: { request?: unknown; accepted?: () => void }) {
  const creation = useFleetClientCreation(true, accepted);
  return <><output data-testid="actor">{creation.actorId}</output><output data-testid="mode">{creation.mode}</output><output data-testid="busy">{String(creation.busy)}</output><output data-testid="loading">{String(creation.loading)}</output>
    <button onClick={() => { void creation.submit(request); }}>Submit draft</button>
    <button onClick={() => { void creation.refreshCapability(); }}>Refresh capability</button>
    <FleetClientCreationStatus creation={creation} onStartNew={() => { creation.startNew(); }} onComplete={() => {}} />
  </>;
}

function renderCreation(request: unknown = creationRequest, actorId = 1, accepted = vi.fn()) {
  const cache = makeTestQueryClient();
  cache.setQueryData(['signed-in-user'], { ...creationAdmin, id: actorId });
  return { ...renderWithProviders(<Harness request={request} accepted={accepted} />, { queryClient: cache }), cache, accepted };
}

type CapabilityValue = boolean | null | Record<string, unknown>;
function server(supported: CapabilityValue = true) {
  let capability = supported;
  let actorId = 1;
  const receipts = new Map<string, unknown>();
  const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async url => {
    if (url === '/panel/api/me') return new Msg(true, '', { ...creationAdmin, id: actorId });
    if (url === `${path}/capabilities`) return capability === null ? new Msg(false) : new Msg(true, '', typeof capability === 'object' ? capability : { supported: capability, actorId });
    const receipt = receipts.get(url);
    return receipt ? new Msg(true, '', receipt) : new Msg(false, 'No receipt');
  });
  return { get, setActor: (value: number) => { actorId = value; }, setCapability: (value: CapabilityValue) => { capability = value; }, receipt: (id: string, result = creationResult) => { receipts.set(`${path}/creation/${id}`, result); } };
}

async function ready(mode = 'fleet') { await waitFor(() => expect(screen.getByTestId('mode').textContent).toBe(mode)); }
async function readyActor(actorId: number) {
  await waitFor(() => expect(screen.getByTestId('actor').textContent).toBe(String(actorId)));
  await ready();
}
async function settled() { await waitFor(() => {
  expect(screen.getByTestId('busy').textContent).toBe('false');
  expect(document.querySelector('button.ant-btn-loading')).toBeNull();
}); }
async function submit() { fireEvent.click(screen.getByRole('button', { name: 'Submit draft' })); await screen.findByText('Client creation is unconfirmed'); await settled(); }
async function confirmNew(container: HTMLElement = document.body) {
  fireEvent.click(within(container).getByRole('button', { name: 'Start a new request' }));
  const title = await screen.findByText('Start a new creation request?');
  fireEvent.click(within(title.closest('.ant-popover')!).getByRole('button', { name: 'Start a new request' }));
  await waitFor(() => expect(within(container).queryByText('Client creation is unconfirmed')).toBeNull());
}

describe('owned single-client creation', () => {
  it('keeps a capability for another actor unavailable until the current actor is refreshed', async () => {
    const api = server(); api.setActor(2);
    const post = vi.spyOn(HttpUtil, 'post');
    renderCreation();
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    expect(screen.getByTestId('actor').textContent).toBe('1');
    expect(screen.getByTestId('mode').textContent).toBe('unavailable');
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh capability' }));
    await readyActor(2);
  });

  it('retains recovery when a receipt response belongs to another actor', async () => {
    const api = server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValue(creationOperationOne);
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    const view = renderCreation(); await ready(); await submit();
    api.receipt(creationOperationOne, { ...creationResult, actorId: 2 });
    fireEvent.click(screen.getByRole('button', { name: 'Check receipt' }));
    await screen.findByText('No creation receipt was confirmed. The request may still complete.');
    await settled();
    expect(view.accepted).not.toHaveBeenCalled();
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationOne);
  });

  it('pins creation to the displayed actor when another tab changes the authenticated session', async () => {
    server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValue(creationOperationOne);
    let authenticatedActor = 1;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (_url, body) => {
      const expectedActor = (body as Record<string, unknown>).expectedActorId;
      return expectedActor !== undefined && expectedActor !== authenticatedActor
        ? new Msg(false, 'Authenticated actor changed') : new Msg(true, '', creationResult);
    });
    const view = renderCreation(); await ready();
    authenticatedActor = 2;
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1)); await settled();
    expect(post.mock.calls[0][1]).toMatchObject({ expectedActorId: 1 });
    expect(view.accepted).not.toHaveBeenCalled();
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationOne);
    expect(sessionStorage.getItem('fleet-client-creation:2')).toBeNull();
  });

  it('distinguishes an unavailable capability from explicit legacy support', async () => {
    const api = server(null);
    const post = vi.spyOn(HttpUtil, 'post');
    renderCreation();
    await screen.findByText('Client creation is unavailable until its capability can be confirmed.');
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    expect(screen.getByTestId('mode').textContent).toBe('unavailable');
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    expect(post).not.toHaveBeenCalled();
    api.setCapability({ supported: 'false' });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh capability' }));
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    expect(screen.getByTestId('mode').textContent).toBe('unavailable');
    api.setCapability(false);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh capability' }));
    await ready('legacy');
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    expect(post).not.toHaveBeenCalled();
  });

  it('retains only a locator and retries the exact nested request including scoped credentials', async () => {
    server();
    const request = structuredClone(creationRequest);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    const random = vi.spyOn(RandomUtil, 'randomUUID').mockReturnValueOnce(creationOperationOne).mockReturnValue(creationOperationTwo);
    const view = renderCreation(request);
    await ready(); await submit();
    const first = structuredClone(post.mock.calls[0][1]);
    expect(first).toMatchObject({ ...creationRequest, operationId: creationOperationOne });
    expect(sessionStorage.getItem(key)).toBe(JSON.stringify({ actorId: 1, activeOperationId: creationOperationOne, operations: [creationOperationOne] }));
    for (const storage of [sessionStorage, localStorage]) {
      const retained = Array.from({ length: storage.length }, (_, index) => storage.getItem(storage.key(index)!)).join('');
      for (const secret of ['shared-private-password', 'private-key-material', 'scoped-private-password', 'scoped-wrapped-key', 'private-token']) expect(retained).not.toContain(secret);
    }
    request.client.password = 'edited-after-send';
    request.credentialBindings![0].values.password = 'different-scoped-value';
    post.mockResolvedValue(new Msg(true, '', creationResult));
    fireEvent.click(screen.getByRole('button', { name: 'Retry the same request' }));
    await waitFor(() => expect(view.accepted).toHaveBeenCalledTimes(1));
    expect(post.mock.calls[1][1]).toEqual(first);
    expect(post.mock.calls[1][0]).toBe(path);
    expect(random).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({ actorId: 1, activeOperationId: null, operations: [] });
  });

  it('uses receipt lookup after reload without rebuilding or resending credentials', async () => {
    const api = server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValue(creationOperationOne);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    const first = renderCreation(); await ready(); await submit(); first.unmount();
    const next = renderCreation(); await ready();
    await screen.findByText('Credentials were not stored in the browser. Check the receipt; this request cannot be resent after a reload.');
    expect(screen.queryByRole('button', { name: 'Retry the same request' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Check receipt' }));
    await screen.findByText('No creation receipt was confirmed. The request may still complete.');
    await settled();
    expect(post).toHaveBeenCalledTimes(1);
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationOne);
    api.receipt(creationOperationOne);
    fireEvent.click(screen.getByRole('button', { name: 'Check receipt' }));
    await waitFor(() => expect(next.accepted).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('requires explicit confirmation for a new ID and retains unresolved old receipts', async () => {
    const api = server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValueOnce(creationOperationOne).mockReturnValueOnce(creationOperationTwo);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    renderCreation(); await ready(); await submit();
    fireEvent.click(screen.getByRole('button', { name: 'Start a new request' }));
    await screen.findByText('Start a new creation request?');
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationOne);
    fireEvent.click(within(screen.getByText('Start a new creation request?').closest('.ant-popover')!).getByRole('button', { name: 'Start a new request' }));
    await waitFor(() => expect(screen.queryByText('Client creation is unconfirmed')).toBeNull());
    await submit();
    expect(post.mock.calls[1][1]).toMatchObject({ operationId: creationOperationTwo, client: { email: creationRequest.client.email, subId: creationRequest.client.subId } });
    expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({ actorId: 1, activeOperationId: creationOperationTwo, operations: [creationOperationOne, creationOperationTwo] });
    api.receipt(creationOperationOne);
    fireEvent.click(screen.getByRole('button', { name: `Check receipt ${creationOperationOne}` })); await settled();
    await waitFor(() => expect(JSON.parse(sessionStorage.getItem(key)!).operations).toEqual([creationOperationTwo]));
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationTwo);
  });

  it('never switches an unresolved owned request to the legacy path when capabilities change', async () => {
    const api = server();
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    renderCreation(); await ready(); await submit();
    api.setCapability(false);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh capability' })); await ready('unavailable');
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Client creation is unconfirmed')).not.toBeNull();
  });

  it('refuses sending when recovery storage fails or another request became active', async () => {
    server();
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    renderCreation(); await ready();
    sessionStorage.setItem(key, JSON.stringify({ actorId: 1, activeOperationId: creationOperationOne, operations: [creationOperationOne] }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await screen.findByText('Client creation is unconfirmed');
    expect(post).not.toHaveBeenCalled();
    await confirmNew();
    const storage = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('storage disabled'); });
    fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await screen.findByText('The recovery reference could not be retained. No new request will be sent.');
    expect(post).not.toHaveBeenCalled();
    storage.mockRestore();
  });

  it('keeps recovery ownership isolated by the authenticated actor', async () => {
    const api = server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValue(creationOperationOne);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    const view = renderCreation(); await ready(); await submit();
    api.setActor(2);
    await act(async () => { view.cache.setQueryData(['signed-in-user'], { ...creationAdmin, id: 2 }); });
    await readyActor(2);
    expect(screen.queryByText(creationOperationOne)).toBeNull();
    expect(post).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem(key)).toContain(creationOperationOne);
    expect(sessionStorage.getItem('fleet-client-creation:2')).toBeNull();
  });

  it('does not let an old completion erase a newer request after remount', async () => {
    server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValueOnce(creationOperationOne).mockReturnValueOnce(creationOperationTwo);
    let resolveOld!: (value: Msg) => void, resolveNew!: (value: Msg) => void;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveOld = resolve; }))
      .mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveNew = resolve; }));
    const first = renderCreation(); await ready(); fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1)); first.unmount();
    renderCreation(); await ready(); await confirmNew(); fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    await act(async () => { resolveOld(new Msg(true, '', creationResult)); });
    expect(JSON.parse(sessionStorage.getItem(key)!).activeOperationId).toBe(creationOperationTwo);
    await act(async () => { resolveNew(new Msg(true, '', creationResult)); });
    await settled();
    expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({ actorId: 1, activeOperationId: null, operations: [creationOperationOne] });
  });

  it('preserves a newer active locator when another live view receives the old completion', async () => {
    server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValueOnce(creationOperationOne).mockReturnValueOnce(creationOperationTwo);
    let resolveOld!: (value: Msg) => void, resolveNew!: (value: Msg) => void;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveOld = resolve; }))
      .mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveNew = resolve; }));
    const first = renderCreation(); await ready(); fireEvent.click(within(first.container).getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    const second = renderCreation();
    await waitFor(() => expect(within(second.container).getByTestId('mode').textContent).toBe('fleet'));
    await confirmNew(second.container);
    fireEvent.click(within(second.container).getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    await act(async () => { resolveOld(new Msg(true, '', creationResult)); });
    expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({ actorId: 1, activeOperationId: creationOperationTwo, operations: [creationOperationTwo] });
    await act(async () => { resolveNew(new Msg(true, '', creationResult)); });
    await waitFor(() => expect(JSON.parse(sessionStorage.getItem(key)!).operations).toEqual([]));
  });

  it('forgets retry material when locator refresh discovers another active request', async () => {
    server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValue(creationOperationOne);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false));
    renderCreation(); await ready(); await submit();
    sessionStorage.setItem(key, JSON.stringify({ actorId: 1, activeOperationId: creationOperationTwo, operations: [creationOperationOne, creationOperationTwo] }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh capability' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry the same request' })).toBeNull());
    expect(screen.getByText('Credentials were not stored in the browser. Check the receipt; this request cannot be resent after a reload.')).toBeTruthy();
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('fences late responses across an actor A to B to A lifetime change', async () => {
    const api = server();
    vi.spyOn(RandomUtil, 'randomUUID').mockReturnValueOnce(creationOperationOne).mockReturnValueOnce(creationOperationTwo);
    let resolveOld!: (value: Msg) => void, resolveNew!: (value: Msg) => void;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveOld = resolve; }))
      .mockImplementationOnce(() => new Promise<Msg>(resolve => { resolveNew = resolve; }));
    const view = renderCreation(); await ready(); fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    api.setActor(2);
    await act(async () => { view.cache.setQueryData(['signed-in-user'], { ...creationAdmin, id: 2 }); }); await readyActor(2);
    api.setActor(1);
    await act(async () => { view.cache.setQueryData(['signed-in-user'], creationAdmin); }); await readyActor(1);
    expect(screen.queryByRole('button', { name: 'Retry the same request' })).toBeNull();
    await confirmNew(); fireEvent.click(screen.getByRole('button', { name: 'Submit draft' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    await act(async () => { resolveOld(new Msg(true, '', creationResult)); });
    expect(screen.getByTestId('busy').textContent).toBe('true');
    expect(JSON.parse(sessionStorage.getItem(key)!)).toEqual({ actorId: 1, activeOperationId: creationOperationTwo, operations: [creationOperationOne, creationOperationTwo] });
    expect(view.accepted).not.toHaveBeenCalled();
    await act(async () => { resolveNew(new Msg(true, '', creationResult)); });
    await settled();
    expect(view.accepted).toHaveBeenCalledTimes(1);
  });
});
