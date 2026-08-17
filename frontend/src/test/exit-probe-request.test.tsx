import type { ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useXraySetting } from '@/hooks/useXraySetting';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';
import { exitKeyFor, exitIdFromKey, isExitKey } from '@/schemas/api/egress';

afterEach(() => { vi.restoreAllMocks(); });

function wrapper() {
  const client = makeTestQueryClient();
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

/*
The exit probe must post JSON.

HttpUtil defaults to x-www-form-urlencoded, which turned {ids:[18]} into
"ids=18&mode=real" and made the Go handler answer "invalid character 'i' looking
for beginning of value" — a decoder error shown to an operator who pressed Test.
No typecheck can see a wire format, so it is pinned by driving the real hook.
*/
describe('the exit probe request', () => {
  it('declares JSON, because the ids are a list that form encoding flattens', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', []) as never);
    const { result } = renderHook(() => useXraySetting(), { wrapper: wrapper() });

    await act(async () => { await result.current.testExit(exitKeyFor(18), 'real'); });

    const call = post.mock.calls.find(([url]) => String(url).includes('/egresses/test'));
    expect(call, 'testExit never posted to /egresses/test').toBeTruthy();
    const [, body, options] = call!;
    expect(body).toEqual({ ids: [18], mode: 'real' });
    expect((options as { headers?: Record<string, string> })?.headers?.['Content-Type'])
      .toBe('application/json');
  });

  /* Test All probes exits too, or the button silently skips every row that is
     not in the xray config — which is every exit. */
  it('probes the exits Test All is handed', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', []) as never);
    const { result } = renderHook(() => useXraySetting(), { wrapper: wrapper() });

    await act(async () => { await result.current.testAllOutbounds('real', [exitKeyFor(18), exitKeyFor(4)]); });

    const ids = post.mock.calls
      .filter(([url]) => String(url).includes('/egresses/test'))
      .map(([, body]) => (body as { ids: number[] }).ids[0]);
    expect(ids.sort((a, b) => a - b)).toEqual([4, 18]);
  });
});

/*
An exit's row key must round-trip to the id the endpoint is asked for, and must
never collide with an outbound's array index — the two share one state map, so a
collision writes one row's result onto another.
*/
describe('the exit key space', () => {
  it('round-trips and stays clear of every outbound index', () => {
    for (const id of [1, 18, 999]) {
      expect(exitIdFromKey(exitKeyFor(id))).toBe(id);
      expect(isExitKey(exitKeyFor(id))).toBe(true);
    }
    for (const index of [0, 1, 50, 9999]) {
      expect(isExitKey(index)).toBe(false);
    }
  });
});
