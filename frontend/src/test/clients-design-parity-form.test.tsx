import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { QueryClient } from '@tanstack/react-query';
import ClientFormModal from '@/pages/clients/ClientFormModal';
import { parityClient, parityCores, parityEnforced, parityExternalLinks, parityInbounds, parityPlans } from '@/pages/clients/ClientsParity.fixtures';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

afterEach(() => vi.restoreAllMocks());

function setup(edit = false, initialPolicyId = 0, pendingPlans?: Promise<Msg>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  queryClient.setQueryData(keys.cores.list(), parityCores);
  if (!pendingPlans) queryClient.setQueryData(keys.policies.list(), parityPlans);
  queryClient.setQueryData(keys.policies.enforced('roham'), parityEnforced);
  queryClient.setQueryData(keys.server.ipLimitStatus(), { enabled: true, enforceable: true, reason: '' });
  vi.spyOn(HttpUtil, 'get').mockImplementation(() => pendingPlans ?? Promise.resolve(new Msg(true, '', [])));
  vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', {}));
  const save = vi.fn().mockResolvedValue({ success: true });
  renderWithProviders(<ClientFormModal open mode={edit ? 'edit' : 'add'} client={edit ? parityClient : null}
    attachedIds={edit ? [1, 2] : []} attachedExternalLinks={edit ? parityExternalLinks : []} initialPolicyId={initialPolicyId}
    inbounds={parityInbounds} tgBotEnable save={save} onOpenChange={() => {}} />, { queryClient });
  return { save, queryClient };
}

describe('Clients design form retains operational state', () => {
  it('does not replace a custom draft when the plan list arrives late', async () => {
    let finish!: (message: Msg) => void;
    const pending = new Promise<Msg>((resolve) => { finish = resolve; });
    const { queryClient } = setup(false, 0, pending);
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment' }), { target: { value: 'Keep my custom draft' } });
    finish(new Msg(true, '', parityPlans));
    await waitFor(() => expect(queryClient.getQueryData(keys.policies.list())).toHaveLength(parityPlans.length));
    expect(document.querySelector('.client-plan-summary')).toBeNull();
    expect((screen.getByRole('textbox', { name: 'Comment' }) as HTMLTextAreaElement).value).toBe('Keep my custom draft');
  });

  it('loads the assigned plan on Basics and preserves hidden credentials, exact quota, and links on save', async () => {
    const { save } = setup(true);
    await waitFor(() => expect(document.querySelector('.client-plan-summary')?.textContent).toContain('50'));
    fireEvent.change(screen.getByRole('textbox', { name: 'Comment' }), { target: { value: 'reviewed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({
      id: parityClient.uuid, password: parityClient.password, subId: parityClient.subId, flow: parityClient.flow,
      totalGB: parityClient.totalGB, tgId: parityClient.tgId, reset: 30, expiryTime: -30 * 86400000, comment: 'reviewed', inboundIds: [1, 2],
    }), expect.objectContaining({ isEdit: true, policyId: undefined, attach: [], detach: [], externalLinks: parityExternalLinks })));
  });

  it('allows a plan-linked create to switch to custom without reapplying the URL plan', async () => {
    const { save } = setup(false, 1);
    await waitFor(() => expect(document.querySelector('.client-plan-summary')).not.toBeNull());
    fireEvent.click(screen.getByText('Custom client', { selector: '.ant-segmented-item-label' }));
    await waitFor(() => expect(document.querySelector('.client-plan-summary')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ inboundIds: [1, 2] }), expect.objectContaining({ isEdit: false, policyId: undefined })));
  });

  it('offers only own active plans and uses their actual quota and attachment values', async () => {
    const { save } = setup();
    await waitFor(() => expect(document.querySelector('.client-plan-summary')).not.toBeNull());
    const plan = screen.getByRole('combobox', { name: 'Plan' });
    fireEvent.mouseDown(plan);
    await waitFor(() => expect(document.querySelector('.ant-select-dropdown')).not.toBeNull());
    expect(document.querySelector('.ant-select-dropdown')?.textContent).not.toContain('Reseller private');
    expect(document.querySelector('.ant-select-dropdown')?.textContent).not.toContain('Retired plan');
    fireEvent.keyDown(plan, { key: 'Escape' });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({
      client: expect.objectContaining({ totalGB: 50 * 1024 ** 3, expiryTime: -30 * 86400000, limitIp: 2 }), inboundIds: [1, 2],
    }), expect.objectContaining({ policyId: 1 })));
  });
});
