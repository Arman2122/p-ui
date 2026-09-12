import type { ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { activityKeys, fetchActivity, fetchAlerts, saveActivityRead } from '@/api/activity';
import {
  clearNotifications,
  markAllRead,
  markRead,
  pushNotification,
  useActivityBridge,
  useNotificationFeed,
  useNotificationState,
} from '@/api/notificationFeed';
import type { ActivityView } from '@/generated/zod';

vi.mock('@/api/activity', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/api/activity')>(),
  fetchActivity: vi.fn(),
  fetchAlerts: vi.fn(),
  saveActivityRead: vi.fn(),
}));

type ActivityPage = Awaited<ReturnType<typeof fetchActivity>>;

const clients: QueryClient[] = [];

function event(id: string, title = `Event ${id}`, read = false): ActivityView {
  return { id, title, read, at: 1789227000000, kind: 'client.updated', category: 'activity',
    level: 'info', message: '', titleKey: '', messageKey: '', params: {} };
}

function page(items: ActivityView[], generation = 'database-A'): ActivityPage {
  return { generation, items, unread: items.filter((item) => !item.read).length, recent: items.length, nextBefore: 0 };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

function mount(viewer = '1:admin') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  clients.push(client);
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook = renderHook(({ viewer }) => {
    useActivityBridge(viewer);
    return { items: useNotificationFeed(), state: useNotificationState() };
  }, { wrapper, initialProps: { viewer } });
  return { ...hook, refresh: () => act(async () => { await client.invalidateQueries({ queryKey: activityKeys.root }); }) };
}

beforeEach(() => {
  clearNotifications();
  vi.mocked(fetchActivity).mockReset().mockResolvedValue(page([]));
  vi.mocked(fetchAlerts).mockReset().mockResolvedValue([]);
  vi.mocked(saveActivityRead).mockReset().mockResolvedValue(true);
});

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  clearNotifications();
});

const readActions = [
  { name: 'one event', run: () => markRead('21'), body: { id: 21 }, expected: [false, true] },
  { name: 'all events', run: () => markAllRead(), body: { throughId: 22 }, expected: [true, true] },
] as const;

describe('persistent activity bridge', () => {
  it.each(readActions)('keeps unread state when saving $name fails', async ({ run, body }) => {
    let response = page([event('22'), event('21')]);
    vi.mocked(fetchActivity).mockImplementation(async () => response);
    vi.mocked(saveActivityRead).mockResolvedValue(false);
    const { result, refresh } = mount();
    await waitFor(() => expect(result.current.items.map((item) => item.id)).toEqual(['22', '21']));

    await act(async () => { await run(); });

    expect(saveActivityRead).toHaveBeenCalledWith(body);
    expect(result.current.items.map((item) => item.read)).toEqual([false, false]);
    expect(result.current.state.unread).toBe(2);
    response = page([event('23'), event('22'), event('21')]);
    await refresh();
    await waitFor(() => expect(result.current.items.map((item) => item.id)).toEqual(['23', '22', '21']));
    expect(result.current.items.map((item) => item.read)).toEqual([false, false, false]);
    expect(result.current.state.unread).toBe(3);
  });

  it.each(readActions)('preserves successfully read $name across stale refresh and server acknowledgement', async ({ run, body, expected }) => {
    let response = page([event('22'), event('21')]);
    vi.mocked(fetchActivity).mockImplementation(async () => response);
    const { result, refresh } = mount();
    await waitFor(() => expect(result.current.items).toHaveLength(2));

    response = page([event('23'), event('22'), event('21')]);
    await act(async () => { await run(); });

    await waitFor(() => expect(result.current.items.map((item) => item.id)).toEqual(['23', '22', '21']));
    expect(saveActivityRead).toHaveBeenCalledWith(body);
    expect(result.current.items.map((item) => item.read)).toEqual([false, ...expected]);

    response = page([event('23'), event('22', 'Event 22', expected[0]), event('21', 'Event 21', expected[1])]);
    await refresh();
    await waitFor(() => expect(result.current.state.unread).toBe(response.unread));
    await refresh();
    expect(result.current.items.map((item) => item.read)).toEqual([false, ...expected]);
    expect(fetchActivity).toHaveBeenCalledTimes(4);
  });

  it('clears retained IDs and both individual and through-ID read masks after a database generation change', async () => {
    let response = page([event('99', 'Old high ID'), event('21', 'Old reused ID')]);
    vi.mocked(fetchActivity).mockImplementation(async () => response);
    const { result, refresh } = mount();
    await waitFor(() => expect(result.current.items).toHaveLength(2));
    await act(async () => { await markRead('21'); await markAllRead(); });
    expect(result.current.items.every((item) => item.read)).toBe(true);

    response = page([event('22', 'Restored event 22'), event('21', 'Restored event 21')], 'database-B');
    await refresh();

    await waitFor(() => expect(result.current.items.map((item) => item.title)).toEqual(['Restored event 22', 'Restored event 21']));
    expect(result.current.items.map((item) => item.read)).toEqual([false, false]);
    expect(result.current.state.unread).toBe(2);
    await refresh();
    expect(result.current.items.map((item) => item.id)).toEqual(['22', '21']);
    expect(result.current.items.every((item) => !item.read)).toBe(true);
  });

  it('clears the previous viewer activity, alerts, local messages and read masks before the next viewer fetch resolves', async () => {
    vi.mocked(fetchActivity).mockResolvedValue(page([event('99', 'Private activity'), event('90')]));
    vi.mocked(fetchAlerts).mockResolvedValue([{ ...event('alert:first', 'Private alert'), category: 'alert', level: 'error' }]);
    const { result, rerender } = mount();
    await waitFor(() => expect(result.current.items).toHaveLength(3));
    await act(async () => { await markRead('90'); await markAllRead(); });
    act(() => pushNotification({ level: 'info', title: 'Private local message' }));

    const nextFeed = deferred<ActivityPage>();
    const nextAlerts = deferred<ActivityView[]>();
    vi.mocked(fetchActivity).mockReturnValueOnce(nextFeed.promise);
    vi.mocked(fetchAlerts).mockReturnValueOnce(nextAlerts.promise);
    rerender({ viewer: '2:reseller' });

    expect(result.current.items).toEqual([]);
    expect(result.current.state).toMatchObject({ ready: false, unread: 0, recent: 0, error: '' });
    await act(async () => {
      nextFeed.resolve(page([event('22', 'Second viewer event 22'), event('21', 'Second viewer event 21')]));
      nextAlerts.resolve([]);
    });

    await waitFor(() => expect(result.current.items.map((item) => item.title)).toEqual(['Second viewer event 22', 'Second viewer event 21']));
    expect(result.current.items.every((item) => !item.read)).toBe(true);
    expect(result.current.state.unread).toBe(2);
  });

  describe.each(['viewer', 'generation'] as const)('in-flight reads across a %s change', (boundary) => {
    it.each(readActions)('does not mark the new feed read when an old save for $name finishes', async ({ run }) => {
      let response = page([event('22'), event('21')]);
      vi.mocked(fetchActivity).mockImplementation(async () => response);
      const saved = deferred<boolean>();
      vi.mocked(saveActivityRead).mockReturnValueOnce(saved.promise);
      const { result, rerender, refresh } = mount();
      await waitFor(() => expect(result.current.items).toHaveLength(2));

      let pending!: Promise<void>;
      act(() => { pending = run(); });
      expect(saveActivityRead).toHaveBeenCalledTimes(1);
      response = page([event('22'), event('21')], boundary === 'generation' ? 'database-B' : 'database-A');
      if (boundary === 'viewer') rerender({ viewer: '2:admin' });
      else await refresh();
      await waitFor(() => expect(fetchActivity).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(result.current.state.ready).toBe(true));
      expect(result.current.items.map((item) => item.id)).toEqual(['22', '21']);
      expect(result.current.items.every((item) => !item.read)).toBe(true);

      await act(async () => { saved.resolve(true); await pending; });

      expect(result.current.items.map((item) => item.read)).toEqual([false, false]);
      await refresh();
      expect(result.current.items.every((item) => !item.read)).toBe(true);
      expect(result.current.state.unread).toBe(2);
    });
  });
});
