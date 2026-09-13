import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { HttpUtil, Msg } from '@/utils';
import { planParityFixtures, planParityInbounds } from '@/pages/policies/PlansParity.fixtures';
import PolicyFormModal from '@/pages/policies/PolicyFormModal';
import GroupFormModal from '@/pages/groups/GroupFormModal';
import { renderWithProviders } from './test-utils';

const headers = { headers: { 'Content-Type': 'application/json' } };
const group = { name: 'Family', quotaBytes: 1073741824, sharedInboundIds: [11, 99], inboundIds: [11, 12, 99], clientCount: 2, trafficUsed: 10, up: 5, down: 5 };

function writeSpy() { return vi.spyOn(HttpUtil, 'post').mockReset().mockResolvedValue(new Msg(true, '', {})); }
function save() { fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: /Save$/ })); }

describe('plans and groups design parity operations', () => {
  it('changes the initial speed using a preset without replacing later tiers or plan lifecycle fields', async () => {
    const post = writeSpy();
    const plan = planParityFixtures()[0];
    renderWithProviders(<PolicyFormModal open policy={plan} inbounds={planParityInbounds} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: '100/25' }));
    save();
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/policies/update/1', expect.objectContaining({
      quotaBytes: plan.quotaBytes, durationDays: 30, limitIp: 2, inboundIds: [11, 12, 13], startOnFirstUse: true, autoRenewDays: 30, resetCycle: 'monthly',
      tiers: [{ fromBytes: 0, upBps: 25_000_000, downBps: 100_000_000 }, { fromBytes: 80 * 1024 ** 3, upBps: 2_000_000, downBps: 5_000_000 }],
    }), headers));
  });

  it('restores the full ladder after turning No shaping on then off', async () => {
    const post = writeSpy(); const plan = planParityFixtures()[0];
    renderWithProviders(<PolicyFormModal open policy={plan} inbounds={planParityInbounds} onClose={vi.fn()} />);
    const toggle = screen.getByText('No shaping').querySelector('button')!;
    fireEvent.click(toggle); fireEvent.click(toggle); save();
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/policies/update/1', expect.objectContaining({ tiers: plan.tiers }), headers));
  });

  it('preserves nullable plan limits and unavailable inbound IDs when editing only the name', async () => {
    const post = writeSpy(); const plan = { ...planParityFixtures()[2], inboundIds: [99] };
    renderWithProviders(<PolicyFormModal open policy={plan} inbounds={planParityInbounds} onClose={vi.fn()} />);
    fireEvent.change(screen.getByRole('textbox', { name: 'Plan name' }), { target: { value: 'Legacy renamed' } }); save();
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/policies/update/3', expect.objectContaining({ name: 'Legacy renamed', tiers: [], quotaBytes: null, durationDays: null, limitIp: null, inboundIds: [99], archived: true }), headers));
  });

  it('renames and changes a group quota without rewriting inherited or unavailable shared inbounds', async () => {
    const post = writeSpy(); const close = vi.fn();
    renderWithProviders(<GroupFormModal group={group} groups={[group]} inbounds={planParityInbounds} onClose={close} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Family renamed' } });
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '2' } }); save();
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
    expect(post.mock.calls.map(([url, body]) => [url, body])).toEqual([
      ['/panel/api/clients/groups/rename', { oldName: 'Family', newName: 'Family renamed' }],
      ['/panel/api/clients/groups/quota', { name: 'Family renamed', quotaBytes: 2147483648 }],
    ]);
  });

  it('retries quota after a partial create without creating the group twice', async () => {
    const post = writeSpy().mockResolvedValueOnce(new Msg(true, '', {})).mockResolvedValueOnce(new Msg(false, 'Quota rejected', {}));
    const close = vi.fn();
    renderWithProviders(<GroupFormModal group={null} groups={[]} inbounds={planParityInbounds} onClose={close} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'New family' } });
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findByText('Quota rejected')).toBeTruthy(); expect(close).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
    expect(post.mock.calls.map(([url, body]) => [url, body])).toEqual([
      ['/panel/api/clients/groups/create', { name: 'New family' }],
      ['/panel/api/clients/groups/quota', { name: 'New family', quotaBytes: 2147483648 }],
      ['/panel/api/clients/groups/quota', { name: 'New family', quotaBytes: 2147483648 }],
    ]);
  });

  it('sends an explicit empty shared inbound set only after the operator clears it', async () => {
    const post = writeSpy();
    renderWithProviders(<GroupFormModal group={group} groups={[group]} inbounds={planParityInbounds} onClose={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Clear' })); save();
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/clients/groups/quota', { name: 'Family', quotaBytes: 1073741824, inboundIds: [] }, headers));
  });

  it('ignores concurrent submits and retries a failed quota without renaming twice', async () => {
    let release: (value: Msg<unknown>) => void = () => {};
    const pending = new Promise<Msg<unknown>>((resolve) => { release = resolve; });
    const post = writeSpy().mockReturnValueOnce(pending).mockResolvedValueOnce(new Msg(false, 'Quota rejected', {}));
    const close = vi.fn();
    renderWithProviders(<GroupFormModal group={group} groups={[group]} inbounds={planParityInbounds} onClose={close} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Renamed once' } });
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '3' } });
    const form = screen.getByRole('textbox').closest('form')!;
    await act(async () => { fireEvent.submit(form); save(); });
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    release(new Msg(true, '', {}));
    expect(await screen.findByText('Quota rejected')).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: /Save$/ }).classList.contains('ant-btn-loading')).toBe(false));
    save();
    await waitFor(() => expect(close).toHaveBeenCalledOnce());
    expect(post.mock.calls.map(([url, body]) => [url, body])).toEqual([
      ['/panel/api/clients/groups/rename', { oldName: 'Family', newName: 'Renamed once' }],
      ['/panel/api/clients/groups/quota', { name: 'Renamed once', quotaBytes: 3221225472 }],
      ['/panel/api/clients/groups/quota', { name: 'Renamed once', quotaBytes: 3221225472 }],
    ]);
  });
});
