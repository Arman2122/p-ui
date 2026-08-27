import type { ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useClients } from '@/hooks/useClients';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';
import type { ClientRecord } from '@/hooks/useClients';

afterEach(() => {
  vi.restoreAllMocks();
});

/*
Flipping one client's enable switch must not go through the editor's endpoint.

setEnable used to rebuild a whole client payload and POST it to
/panel/api/clients/update. That endpoint reads an ABSENT adTag as "clear it"
(client_crud.go:517) while an absent secret is restored (:383) — an asymmetry
that is correct for the editor, where clearing a tag has to be possible, and
destructive for a toggle. One flip of the switch hard-deleted an MTProto
client's ad tag on every inbound it was attached to, with nothing to undo it.

Re-sending the tag would not have fixed it. ClientRecord carries no adTag
column, so the value is not on the row the toggle holds; and the credential map
flattens every inbound first-write-wins, so echoing one back would stamp a
single inbound's tag onto its siblings — a silent overwrite traded for a silent
delete.

So the toggle uses the field-level endpoint instead, whose handler touches only
`enable` and `updated_at` (client_bulk.go:1588-1589) and never reads or writes
client_credentials at all. F169.
*/

const ROW = {
  email: 'alice@example.com',
  enable: true,
} as unknown as ClientRecord;

function wrapper({ children }: { children: ReactNode }) {
  const client = makeTestQueryClient();
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

/* Records every request the hook makes, and answers each one plausibly. */
function recordRequests() {
  const posts: { url: string; body: unknown }[] = [];
  const gets: string[] = [];

  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    gets.push(url);
    return new Msg(true, '', {});
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string, body?: unknown) => {
    posts.push({ url, body });
    return new Msg(true, '', { changed: 1, skipped: [] });
  });

  return { posts, gets };
}

describe('toggling one client', () => {
  it('uses the field-level endpoint, never the editor update', async () => {
    const { posts, gets } = recordRequests();
    const { result } = renderHook(() => useClients(), { wrapper });

    await act(async () => {
      await result.current.setEnable(ROW, false);
    });

    const togglePosts = posts.filter((p) => /bulkEnable|bulkDisable|clients\/update/.test(p.url));
    expect(togglePosts).toHaveLength(1);
    expect(togglePosts[0].url).toContain('/panel/api/clients/bulkDisable');
    expect(togglePosts[0].body).toEqual({ emails: ['alice@example.com'] });

    // The editor endpoint is what deletes the ad tag; it must not be reached.
    expect(posts.some((p) => p.url.includes('/panel/api/clients/update'))).toBe(false);
    // And the hydrate GET the old path needed is gone with it.
    expect(gets.some((url) => url.includes('/panel/api/clients/get/'))).toBe(false);
  });

  it('enables through the enable endpoint', async () => {
    const { posts } = recordRequests();
    const { result } = renderHook(() => useClients(), { wrapper });

    await act(async () => {
      await result.current.setEnable(ROW, true);
    });

    const toggle = posts.find((p) => /bulkEnable|bulkDisable/.test(p.url));
    expect(toggle?.url).toContain('/panel/api/clients/bulkEnable');
  });
});
