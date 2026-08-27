import type { ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useClients } from '@/hooks/useClients';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => {
  vi.restoreAllMocks();
});

/*
Resetting the traffic of the clients you selected.

The endpoint, the service method and the core-quota reset have all shipped since
the bulk API landed, and nothing on the frontend referenced bulkResetTraffic --
grep found zero hits. So the panel offered "reset this one client" on a row and
"reset EVERY client on the panel" in the menu, with nothing in between, and an
operator who wanted to reset forty renewed subscriptions had only the panel-wide
button to reach for.

That is the asymmetry this closes. It matters which endpoint is used: the
panel-wide one takes no emails and zeroes everyone.
*/

function wrapper({ children }: { children: ReactNode }) {
  const client = makeTestQueryClient();
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function recordPosts() {
  const posts: { url: string; body: unknown }[] = [];
  vi.spyOn(HttpUtil, 'get').mockImplementation(async () => new Msg(true, '', {}));
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string, body?: unknown) => {
    posts.push({ url, body });
    return new Msg(true, '', { affected: 2 });
  });
  return posts;
}

describe('bulkResetTraffic', () => {
  it('resets only the emails it is given', async () => {
    const posts = recordPosts();
    const { result } = renderHook(() => useClients(), { wrapper });

    await act(async () => {
      await result.current.bulkResetTraffic(['alice@example.com', 'bob@example.com']);
    });

    const reset = posts.filter((p) => /resetTraffic|resetAllTraffics/i.test(p.url));
    expect(reset, 'the hook made no reset request at all').toHaveLength(1);
    expect(reset[0].url).toContain('/panel/api/clients/bulkResetTraffic');
    expect(reset[0].body).toEqual({ emails: ['alice@example.com', 'bob@example.com'] });

    // The panel-wide endpoint takes no emails and would zero every client.
    expect(posts.some((p) => p.url.includes('/panel/api/clients/resetAllTraffics'))).toBe(false);
  });

  it('does nothing at all for an empty selection', async () => {
    const posts = recordPosts();
    const { result } = renderHook(() => useClients(), { wrapper });

    await act(async () => {
      await result.current.bulkResetTraffic([]);
    });

    expect(posts.filter((p) => /reset/i.test(p.url))).toHaveLength(0);
  });
});
