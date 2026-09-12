import type { ReactNode } from 'react';
import { renderHook, act, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useRoutingDraft } from '@/hooks/useRoutingDraft';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';
import type { RoutingRulePayload } from '@/schemas/api/routing';

afterEach(() => {
  vi.restoreAllMocks();
});

/*
A routing rule is a draft until Save, like everything else on the Xray page.

It used to write straight through. The dialog's OK posted the rule; "move up"
posted a new order. Every one of those endpoints raises the restart flag, so the
change reached the RUNNING config at the next reconcile -- and routing is
first-match, which makes nudging a row up a live routing change on real traffic.

There was no review step and no way to ask for one: the page's Save button
watches the template, and these rules are not in it, so it stayed disabled while
the change had already happened. An operator mid-way through composing a rule
had already applied it.

Now the edits sit in a draft, Save lights up, and nothing is written until it is
pressed.
*/

const SERVER_RULE = {
  id: 5,
  sortIndex: 0,
  enable: true,
  remark: 'existing',
  ingressScope: 'all',
  ingressIds: '[]',
  destKind: 'outbound',
  destTag: 'direct',
  destExitId: null,
  criteria: '{}',
  inspect: false,
};

const NEW_RULE: RoutingRulePayload = {
  enable: true,
  remark: 'brand new',
  ingressScope: 'all',
  ingressIds: '[]',
  destKind: 'outbound',
  destTag: 'blocked',
  destExitId: null,
  criteria: '{}',
  inspect: false,
};

function wrapper({ children }: { children: ReactNode }) {
  const client = makeTestQueryClient();
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function recordCalls() {
  const posts: { url: string; body: unknown }[] = [];
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url.includes('/routing/rules')) return new Msg(true, '', [SERVER_RULE]);
    return new Msg(true, '', []);
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string, body?: unknown) => {
    posts.push({ url, body });
    return new Msg(true, '', { id: 99 });
  });
  return posts;
}

describe('routing rules are drafted, not applied on the spot', () => {
  it('writes nothing when a rule is added', async () => {
    const posts = recordCalls();
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.addRule(NEW_RULE));

    expect(
      posts.filter((p) => p.url.includes('/routing/rules')),
      'adding a rule wrote to the server before Save; every routing write raises the restart flag, so it reaches live traffic at the next reconcile',
    ).toHaveLength(0);
    expect(result.current.rules).toHaveLength(2);
    expect(result.current.dirty, 'the Save button would stay dark with an unsaved rule on screen').toBe(true);
  });

  it('writes nothing when priority is reordered', async () => {
    const posts = recordCalls();
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.addRule(NEW_RULE));
    act(() => result.current.reorderRules([...result.current.rules].reverse()));

    expect(
      posts.filter((p) => p.url.includes('order')),
      'reordering posted a new order before Save; routing is first-match, so that is a live routing change',
    ).toHaveLength(0);
    expect(result.current.rules[0].remark).toBe('brand new');
  });

  it('writes everything once, in order, on flush', async () => {
    const posts = recordCalls();
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.addRule(NEW_RULE));
    act(() => result.current.reorderRules([...result.current.rules].reverse()));

    await act(async () => { await result.current.flush(); });

    const urls = posts.map((p) => p.url);
    expect(urls.some((u) => u.endsWith('/panel/api/routing/rules')), 'the new rule was never created').toBe(true);
    expect(urls.some((u) => u.includes('/order')), 'the order was never sent').toBe(true);
    /* The created rule comes back as id 99 and must lead, because that is where
       the operator dragged it. */
    const order = posts.find((p) => p.url.includes('/order'))?.body as { ids: number[] } | undefined;
    expect(order?.ids[0]).toBe(99);
    expect(result.current.dirty).toBe(false);
  });

  it('does not touch the order when only a remark changed', async () => {
    const posts = recordCalls();
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.updateRule(5, { ...NEW_RULE, remark: 'renamed' }));
    await act(async () => { await result.current.flush(); });

    expect(
      posts.filter((p) => p.url.includes('/order')),
      'a remark edit reordered nothing, but sent an order anyway; that endpoint reloads the core',
    ).toHaveLength(0);
  });

  it('deletes before it adds, so a freed name can be reused in one save', async () => {
    const posts = recordCalls();
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.removeRule(5));
    act(() => result.current.addRule(NEW_RULE));
    await act(async () => { await result.current.flush(); });

    const routingPosts = posts.filter((p) => p.url.includes('/routing/rules'));
    const delAt = routingPosts.findIndex((p) => p.url.includes('/del'));
    const addAt = routingPosts.findIndex((p) => p.url.endsWith('/panel/api/routing/rules'));
    expect(delAt, 'the delete never happened').toBeGreaterThanOrEqual(0);
    expect(addAt, 'the add never happened').toBeGreaterThanOrEqual(0);
    expect(delAt).toBeLessThan(addAt);
  });
});

/*
A rule the server refuses stays in the draft.

HttpUtil resolves on a refusal rather than throwing, so an unchecked flush would
walk straight past it, clear the draft and leave the operator with an empty form
and a toast they may already have dismissed. The panel refuses routing rules at
save time on purpose -- a rule the compiler would drop is rejected -- so this is
a path operators reach, not a theoretical one.

The rule stays on screen and Save stays lit, which is the whole point of having a
draft. F191, whose original complaint (the modal closing before the server
answered) no longer applies now that the dialog talks to nobody.
*/
describe('a refused write', () => {
  it('keeps the draft so the rule is still there to correct', async () => {
    vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
      if (url.includes('/routing/rules')) return new Msg(true, '', [SERVER_RULE]);
      return new Msg(true, '', []);
    });
    vi.spyOn(HttpUtil, 'post').mockImplementation(async () => new Msg(false, 'rule refused', null));

    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));

    act(() => result.current.addRule(NEW_RULE));
    await act(async () => { await expect(result.current.flush()).rejects.toThrow('rule refused'); });

    expect(
      result.current.dirty,
      'the draft was cleared after the server refused the rule, so the operator lost what they typed',
    ).toBe(true);
    expect(result.current.rules).toHaveLength(2);
  });

  it('retains a created ID after refused ordering and updates that same rule on retry', async () => {
    recordCalls();
    let refuseOrder = true;
    const post = vi.mocked(HttpUtil.post).mockImplementation(async (url) => {
      if (url.endsWith('/order') && refuseOrder) return new Msg(false, 'order refused', null);
      return new Msg(true, '', { id: 99 });
    });
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));
    act(() => result.current.addRule(NEW_RULE));
    act(() => result.current.reorderRules([...result.current.rules].reverse()));

    await act(async () => { await expect(result.current.flush()).rejects.toThrow('order refused'); });
    expect(result.current.rules.map((rule) => rule.id)).toEqual([99, 5]);
    expect(result.current.dirty).toBe(true);
    act(() => result.current.updateRule(99, { ...NEW_RULE, remark: 'corrected' }));
    refuseOrder = false;
    await act(async () => { await result.current.flush(); });

    expect(post.mock.calls.filter(([url]) => url.endsWith('/routing/rules'))).toHaveLength(1);
    expect(post).toHaveBeenCalledWith('/panel/api/routing/rules/99', expect.objectContaining({ remark: 'corrected' }), expect.anything());
    expect(post).toHaveBeenLastCalledWith('/panel/api/routing/rules/order', { ids: [99, 5] }, expect.anything());
    expect(result.current.dirty).toBe(false);
  });

  it('retains the persisted ID when creation reports a convergence failure', async () => {
    recordCalls();
    const post = vi.mocked(HttpUtil.post).mockImplementation(async (url) => {
      if (url.endsWith('/routing/rules')) return new Msg(false, 'core convergence failed', { id: 99 });
      return new Msg(true, '', { id: 99 });
    });
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));
    act(() => result.current.addRule(NEW_RULE));

    await act(async () => { await expect(result.current.flush()).rejects.toThrow('core convergence failed'); });
    expect(result.current.rules[1].id).toBe(99);
    expect(result.current.dirty).toBe(true);
    await act(async () => { await result.current.flush(); });

    expect(post.mock.calls.filter(([url]) => url.endsWith('/routing/rules'))).toHaveLength(1);
    expect(post).toHaveBeenCalledWith('/panel/api/routing/rules/99', NEW_RULE, expect.anything());
    expect(result.current.dirty).toBe(false);
  });

  it('retries a refused update even when the operator restores the original values', async () => {
    recordCalls();
    const post = vi.mocked(HttpUtil.post).mockResolvedValueOnce(new Msg(false, 'core convergence failed', { id: 5 }));
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));
    act(() => result.current.updateRule(5, NEW_RULE));
    await act(async () => { await expect(result.current.flush()).rejects.toThrow('core convergence failed'); });
    act(() => result.current.updateRule(5, { ...NEW_RULE, remark: SERVER_RULE.remark, destTag: SERVER_RULE.destTag }));
    await act(async () => { await result.current.flush(); });

    expect(post.mock.calls.filter(([url]) => url.endsWith('/rules/5'))).toHaveLength(2);
    expect(post).toHaveBeenLastCalledWith('/panel/api/routing/rules/5', expect.objectContaining({ remark: 'existing', destTag: 'direct' }), expect.anything());
    expect(result.current.dirty).toBe(false);
  });

  it('retries convergence after deletion persisted, even when the refreshed list is empty', async () => {
    let rules = [SERVER_RULE];
    const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async () => new Msg(true, '', rules));
    let converged = false;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async () => {
      const alreadyDeleted = rules.length === 0;
      rules = [];
      converged = alreadyDeleted;
      return new Msg(alreadyDeleted, alreadyDeleted ? '' : 'core convergence failed', null);
    });
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));
    act(() => result.current.removeRule(5));
    await act(async () => { await expect(result.current.flush()).rejects.toThrow('core convergence failed'); });
    expect(rules).toHaveLength(0);
    expect(converged).toBe(false);
    await act(async () => { result.current.retry?.(); });
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(result.current.dirty).toBe(true);

    await act(async () => { await result.current.flush(); });

    expect(post.mock.calls.map(([url]) => url)).toEqual(['/panel/api/routing/rules/5/del', '/panel/api/routing/rules/5/del']);
    expect(converged).toBe(true);
    expect(result.current.rules).toHaveLength(0);
    expect(result.current.dirty).toBe(false);
  });

  it.each(['refusal', 'transport error'])('keeps acknowledged deletes and creates before a later %s', async (failure) => {
    recordCalls();
    let rejectSecond = true;
    const post = vi.mocked(HttpUtil.post).mockImplementation(async (url, body) => {
      if (url.endsWith('/routing/rules') && (body as RoutingRulePayload).remark === 'second') {
        if (rejectSecond) {
          if (failure === 'transport error') throw new Error('connection lost');
          return new Msg(false, 'second refused', null);
        }
        return new Msg(true, '', { id: 100 });
      }
      return new Msg(true, '', { id: 99 });
    });
    const { result } = renderHook(() => useRoutingDraft(), { wrapper });
    await waitFor(() => expect(result.current.rules).toHaveLength(1));
    act(() => result.current.removeRule(5));
    act(() => result.current.addRule(NEW_RULE));
    act(() => result.current.addRule({ ...NEW_RULE, remark: 'second' }));

    await act(async () => { await expect(result.current.flush()).rejects.toThrow(failure === 'refusal' ? 'second refused' : 'connection lost'); });
    expect(result.current.rules[0].id).toBe(99);
    expect(result.current.rules[1].id).toBeLessThan(0);
    expect(post.mock.calls.filter(([url]) => url.endsWith('/order'))).toHaveLength(0);
    expect(result.current.dirty).toBe(true);
    rejectSecond = false;
    await act(async () => { await result.current.flush(); });

    expect(post.mock.calls.filter(([url]) => url.endsWith('/5/del'))).toHaveLength(1);
    expect(post.mock.calls.filter(([url, body]) => url.endsWith('/routing/rules') && (body as RoutingRulePayload).remark === NEW_RULE.remark)).toHaveLength(1);
    expect(post.mock.calls.filter(([url, body]) => url.endsWith('/routing/rules') && (body as RoutingRulePayload).remark === 'second')).toHaveLength(2);
    expect(result.current.dirty).toBe(false);
  });
});
