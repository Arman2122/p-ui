import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, expect, it, vi } from 'vitest';
import { useInbounds } from '@/pages/inbounds/useInbounds';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient } from './test-utils';

afterEach(() => { vi.restoreAllMocks(); });

it('pairs raw counters with successful query timestamps and leaves missing or failed readings unknown', async () => {
  const clock = vi.spyOn(Date, 'now').mockReturnValue(1000);
  let payload: unknown[] = [{ id: 7, protocol: 'vless', down: 100, settings: { clients: [] } }];
  let failed = false;
  vi.spyOn(HttpUtil, 'get').mockImplementation(async () => new Msg(!failed, failed ? 'unavailable' : '', payload));
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => new Msg(true, '', url.endsWith('/onlines') ? [] : {}));
  const query = makeTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={query}>{children}</QueryClientProvider>;
  const { result, rerender } = renderHook(useInbounds, { wrapper });
  await waitFor(() => expect(result.current.fetched).toBe(true));
  expect(result.current.trafficSampleAt).toBe(1000);
  expect(result.current.inboundTrends['7'].points).toEqual([]);
  expect(result.current.inboundTrends['7'].status).toBe('gap');

  async function fresh(at: number, up: number, down: number) {
    clock.mockReturnValue(at);
    payload = [{ id: 7, protocol: 'vless', up, down, settings: { clients: [] } }];
    await act(async () => { await query.refetchQueries({ queryKey: keys.inbounds.slim() }); });
    await waitFor(() => expect(result.current.trafficSampleAt).toBe(at));
  }
  await fresh(11000, 0, 100);
  await fresh(21000, 100, 200);
  await fresh(31000, 200, 300);
  expect(result.current.inboundTrends['7'].points.map((point) => point.bytesPerSecond)).toEqual([null, 20, 20]);
  rerender();
  expect(result.current.inboundTrends['7'].points).toHaveLength(3);

  failed = true; clock.mockReturnValue(41000);
  await act(async () => { await query.refetchQueries({ queryKey: keys.inbounds.slim() }); });
  await waitFor(() => expect(result.current.trafficSampleAt).toBe(0));
  expect(result.current.inboundTrends['7'].status).toBe('gap');
  failed = false;
  await fresh(51000, 10000, 20000);
  expect(result.current.inboundTrends['7'].points.at(-1)?.bytesPerSecond).toBeNull();
});
