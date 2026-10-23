import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { expect, it } from 'vitest';
import { useInbounds } from '@/pages/inbounds/useInbounds';
import { keys } from '@/api/queryKeys';
import { makeTestQueryClient } from './test-utils';

async function loadedInbounds() {
  const cache = makeTestQueryClient();
  cache.setQueryData(keys.inbounds.slim(), [{ id: 1, protocol: 'vless', up: 10, down: 20, total: 0, enable: true,
    settings: { clients: [{ email: 'one', enable: true }] },
    clientStats: [{ email: 'one', up: 1, down: 2, total: 0, expiryTime: 0, enable: true }],
  }]);
  cache.setQueryData(keys.settings.defaults(), {});
  cache.setQueryData(keys.clients.onlines(), []);
  cache.setQueryData(keys.clients.onlinesByGuid(), {});
  cache.setQueryData(keys.clients.activeInbounds(), {});
  cache.setQueryData(keys.clients.lastOnline(), {});
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={cache}>{children}</QueryClientProvider>;
  const hook = renderHook(useInbounds, { wrapper });
  await waitFor(() => expect(hook.result.current.dbInbounds).toHaveLength(1));
  return hook.result;
}

it('keeps table and rollup state for identical snapshots while applying changed counters', async () => {
  const result = await loadedInbounds();
  const before = result.current;
  const event = { inbounds: [{ id: 1, up: 10, down: 20, total: 0, enable: true }], clients: [{ email: 'one', up: 1, down: 2, total: 0, expiryTime: 0, enable: true }] };
  act(() => result.current.applyClientStatsEvent(event));
  expect(result.current.dbInbounds).toBe(before.dbInbounds);
  expect(result.current.clientCount).toBe(before.clientCount);
  expect(result.current.statsVersion).toBe(before.statsVersion);
  act(() => result.current.applyClientStatsEvent({ ...event, clients: [{ ...event.clients[0], up: 5 }] }));
  expect(result.current.statsVersion).toBe(before.statsVersion + 1);
  expect(result.current.dbInbounds[0].clientStats?.[0]?.up).toBe(5);
});

it('keeps online rollups for repeated sets and updates them when membership changes', async () => {
  const result = await loadedInbounds();
  const event = { onlineClients: ['one'], onlineByGuid: { local: ['one'] }, activeInbounds: { local: ['in-1'] } };
  act(() => result.current.applyTrafficEvent(event));
  const before = result.current;
  act(() => result.current.applyTrafficEvent(event));
  expect(result.current.clientCount).toBe(before.clientCount);
  expect(result.current.onlineClients).toBe(before.onlineClients);
  act(() => result.current.applyTrafficEvent({ ...event, onlineClients: [] }));
  expect(result.current.onlineClients).toEqual([]);
  expect(result.current.clientCount).not.toBe(before.clientCount);
});
