import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { ResetScheduleRequestSchema, type ResetScheduleView } from '@/generated/zod';
import { ClientRecordSchema } from '@/schemas/client';
import ClientInfoModal from '@/pages/clients/ClientInfoModal';
import { resetAdmin, resetClient, resetClientId, resetOperationId, resetView } from '@/pages/clients/FleetResetSchedule.fixtures';
import { fleetClient } from '@/pages/clients/ClientsFleet.fixtures';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

const path = `/panel/api/fleet/clients/${resetClientId}/reset-schedule`;
afterEach(() => { sessionStorage.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function renderDetails(client = resetClient, role: 'admin' | 'reseller' = 'admin', actorId = 1) {
  const cache = makeTestQueryClient();
  cache.setQueryData(['signed-in-user'], { ...resetAdmin, id: actorId, role });
  return renderWithProviders(<ClientInfoModal open client={client} inboundsById={{}} isOnline={false} onOpenChange={() => {}} />, { queryClient: cache });
}

function server(initial: ResetScheduleView = resetView) {
  let current = { ...initial };
  const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async url => new Msg(true, '', url.endsWith('/reset-schedule') ? current : []));
  return { get, set: (view: ResetScheduleView) => { current = view; } };
}

async function selectPolicy(label: string) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Reset policy' }).closest('.ant-select')!);
  fireEvent.click(await screen.findByText(label, { selector: '.ant-select-item-option-content' }));
}

async function ready() {
  await waitFor(() => expect((screen.getByRole('combobox', { name: 'Reset policy' }) as HTMLInputElement).disabled).toBe(false));
}

describe('shared fleet reset schedule', () => {
  it('uses the accounting client identity and authenticated admin role, leaving legacy and reseller clients unchanged', async () => {
    const api = server();
    vi.stubGlobal('PUI_ROLE', 'admin');
    const reseller = renderDetails(resetClient, 'reseller');
    await screen.findByRole('dialog');
    expect(screen.queryByRole('region', { name: 'Shared reset schedule' })).toBeNull();
    expect(api.get.mock.calls.some(([url]) => url.includes('/reset-schedule'))).toBe(false);
    reseller.unmount();
    const legacy = renderDetails(fleetClient);
    await screen.findByRole('dialog');
    expect(screen.queryByRole('region', { name: 'Shared reset schedule' })).toBeNull();
    expect(api.get.mock.calls.some(([url]) => url.includes('/reset-schedule'))).toBe(false);
    legacy.unmount();
    renderDetails();
    await ready();
    expect(api.get).toHaveBeenCalledWith(path, undefined, expect.objectContaining({ silent: true }));
    const section = within(screen.getByRole('region', { name: 'Shared reset schedule' }));
    expect(section.getByText('From applied plan')).toBeTruthy();
    expect(section.getByText('Cadence anchor (UTC)').closest('tr')?.textContent).toContain('09:30');
    expect(section.getByText('Cadence anchor (UTC)').closest('tr')?.textContent).toContain('UTC');
  });

  it('omits cycle when inheriting the applied plan and refreshes after the accepted change', async () => {
    const api = server({ ...resetView, inherited: false, cycle: 'daily' });
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (_url, body) => {
      const request = ResetScheduleRequestSchema.parse(body);
      expect(request).toMatchObject({ clientId: resetClientId, expectedRevision: 7, override: false });
      expect(request).not.toHaveProperty('cycle');
      const updated = { ...resetView, revision: 8 };
      api.set(updated);
      return new Msg(true, '', updated);
    });
    renderDetails();
    await ready();
    await selectPolicy('Inherit applied plan');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Schedule change accepted');
    await waitFor(() => expect(api.get.mock.calls.filter(([url]) => url === path)).toHaveLength(2));
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('can initialize an inherited schedule when the read-only view has revision zero', async () => {
    const initial = { ...resetView, revision: 0, anchorUnixMilli: 0, nextDueUnixMilli: 0, nextAttemptUnixMilli: 0 };
    const api = server(initial);
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (_url, body) => {
      const request = ResetScheduleRequestSchema.parse(body);
      expect(request).toMatchObject({ expectedRevision: 0, override: false });
      expect(request).not.toHaveProperty('cycle');
      const updated = { ...initial, revision: 1 };
      api.set(updated);
      return new Msg(true, '', updated);
    });
    renderDetails();
    await ready();
    expect((screen.getByRole('button', { name: 'Save reset schedule' }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Schedule change accepted');
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('retains the exact unconfirmed request across closing and reopening without automatically retrying it', async () => {
    const api = server();
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Response lost'));
    const first = renderDetails();
    await ready();
    await selectPolicy('Daily');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Request outcome unconfirmed');
    const request = ResetScheduleRequestSchema.parse(post.mock.calls[0][1]);
    expect(request).toMatchObject({ clientId: resetClientId, expectedRevision: 7, override: true, cycle: 'daily' });
    expect((screen.getByRole('combobox', { name: 'Reset policy' }) as HTMLInputElement).disabled).toBe(true);
    first.unmount();
    renderDetails();
    await screen.findByText('Request outcome unconfirmed');
    await waitFor(() => expect((screen.getByRole('button', { name: 'Retry same request' }) as HTMLButtonElement).disabled).toBe(false));
    expect(post).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Requested policy: Daily')).toBeTruthy();
    post.mockImplementation(async () => {
      const updated = { ...resetView, inherited: false, cycle: 'daily', revision: 8 };
      api.set(updated);
      return new Msg(true, '', updated);
    });
    fireEvent.click(screen.getByRole('button', { name: 'Retry same request' }));
    await screen.findByText('Schedule change accepted');
    expect(post.mock.calls[1][1]).toEqual(request);
    await waitFor(() => expect(screen.queryByText('Request outcome unconfirmed')).toBeNull());
  });

  it('keeps equal-revision uncertainty and reconciles a newer GET before allowing a new operation', async () => {
    const api = server();
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Response lost'));
    renderDetails();
    await ready();
    await selectPolicy('Daily');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Request outcome unconfirmed');
    const first = ResetScheduleRequestSchema.parse(post.mock.calls[0][1]);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh reset schedule' }));
    await waitFor(() => expect(api.get.mock.calls.filter(([url]) => url === path)).toHaveLength(2));
    expect((screen.getByRole('combobox', { name: 'Reset policy' }) as HTMLInputElement).disabled).toBe(true);
    expect(post).toHaveBeenCalledTimes(1);
    api.set({ ...resetView, inherited: false, cycle: 'monthly', revision: 9 });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh reset schedule' }));
    await screen.findByText('Current schedule refreshed; review it before making another change');
    await ready();
    await selectPolicy('Hourly');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    const next = ResetScheduleRequestSchema.parse(post.mock.calls[1][1]);
    expect(next.expectedRevision).toBe(9);
    expect(next.operationId).not.toBe(first.operationId);
  });

  it('keeps a pending reset read-only and cancels only after explicit confirmation with the same retry identity', async () => {
    const pending = { ...resetView, pendingOperationId: resetOperationId, diagnostic: 'SOURCE_CHANGED' };
    const api = server(pending);
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Response lost'));
    renderDetails();
    const cancel = await screen.findByRole('button', { name: 'Cancel pending reset' });
    await waitFor(() => expect((cancel as HTMLButtonElement).disabled).toBe(false));
    expect((screen.getByRole('combobox', { name: 'Reset policy' }) as HTMLInputElement).disabled).toBe(true);
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(cancel);
    await screen.findByText('Cancel this pending reset?');
    expect(screen.getByText(/Keep the current allowance, used traffic and reservations/)).toBeTruthy();
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel this reset' }));
    await screen.findByText('Request outcome unconfirmed');
    const request = post.mock.calls[0][1];
    expect(request).toMatchObject({ clientId: resetClientId, expectedRevision: 7, pendingOperationId: resetOperationId });
    post.mockImplementation(async () => {
      const cancelled = { ...resetView, revision: 8, diagnostic: 'RESET_CANCELLED', nextDueUnixMilli: resetView.nextDueUnixMilli + 7 * 86400000 };
      api.set(cancelled);
      return new Msg(true, '', cancelled);
    });
    fireEvent.click(screen.getByRole('button', { name: 'Retry same request' }));
    await waitFor(() => expect(api.get.mock.calls.filter(([url]) => url === path)).toHaveLength(2));
    expect(post.mock.calls[1]).toEqual([`${path}/cancel`, request, { silent: true }]);
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancel pending reset' })).toBeNull());
  });

  it('never sends a request if browser retention fails or the loaded identity is foreign', async () => {
    const api = server({ ...resetView, clientId: resetOperationId });
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', resetView));
    const foreign = renderDetails();
    await screen.findByText('Unable to load the reset schedule');
    expect(screen.queryByRole('button', { name: 'Save reset schedule' })).toBeNull();
    expect(post).not.toHaveBeenCalled();
    foreign.unmount();
    api.set(resetView);
    vi.stubGlobal('sessionStorage', { length: 0, clear() {}, key: () => null, getItem: () => null, removeItem() {}, setItem() { throw new Error('Storage blocked'); } } satisfies Storage);
    renderDetails();
    await ready();
    await selectPolicy('Daily');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('This browser cannot safely retain the request. Schedule changes are unavailable.');
    expect(post).not.toHaveBeenCalled();
  });

  it('does not transfer a retained operation to another administrator or client', async () => {
    const api = server();
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Response lost'));
    const first = renderDetails();
    await ready();
    await selectPolicy('Daily');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Request outcome unconfirmed');
    first.unmount();
    const otherAdmin = renderDetails(resetClient, 'admin', 2);
    await ready();
    expect(screen.queryByText('Request outcome unconfirmed')).toBeNull();
    otherAdmin.unmount();
    const otherClient = ClientRecordSchema.parse({ ...resetClient, traffic: { ...resetClient.traffic, accounting: { ...resetClient.traffic?.accounting, clientId: resetOperationId } } });
    api.set({ ...resetView, clientId: resetOperationId });
    renderDetails(otherClient);
    await ready();
    expect(screen.queryByText('Request outcome unconfirmed')).toBeNull();
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('does not let a late response erase a newer retained request after reopening', async () => {
    const api = server();
    let resolveFirst: (response: Msg<ResetScheduleView>) => void = () => {};
    const pendingFirst = new Promise<Msg<ResetScheduleView>>(resolve => { resolveFirst = resolve; });
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'Response lost')).mockImplementationOnce(() => pendingFirst);
    const first = renderDetails();
    await ready();
    await selectPolicy('Daily');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    first.unmount();
    const acceptedFirst = { ...resetView, revision: 8, inherited: false, cycle: 'daily' };
    api.set(acceptedFirst);
    const second = renderDetails();
    await ready();
    await selectPolicy('Monthly');
    fireEvent.click(screen.getByRole('button', { name: 'Save reset schedule' }));
    await screen.findByText('Request outcome unconfirmed');
    const newer = post.mock.calls[1][1];
    await act(async () => { resolveFirst(new Msg(true, '', acceptedFirst)); });
    second.unmount();
    renderDetails();
    await screen.findByText('Request outcome unconfirmed');
    await waitFor(() => expect((screen.getByRole('button', { name: 'Retry same request' }) as HTMLButtonElement).disabled).toBe(false));
    expect(screen.getByText('Requested policy: Monthly')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry same request' }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(3));
    expect(post.mock.calls[2][1]).toEqual(newer);
  });
});
