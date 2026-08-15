import type { ReactNode } from 'react';
import { renderHook, act } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useEgressMutations } from '@/api/queries/useEgressMutations';
import { EGRESS_TYPE_UPLINK } from '@/schemas/api/egress';
import type { EgressPayload } from '@/schemas/api/egress';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => {
  vi.restoreAllMocks();
});

const uplink: EgressPayload = {
  id: 0,
  type: EGRESS_TYPE_UPLINK,
  remark: 'mullvad',
  target: '',
  enable: true,
  owner: 'operator',
  ingressInboundId: 0,
  settings: '{}',
};

function harness() {
  const queryClient = makeTestQueryClient();
  const invalidated = vi.spyOn(queryClient, 'invalidateQueries');
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useEgressMutations(), { wrapper });
  return { result, invalidated };
}

function invalidatedRoots(spy: { mock: { calls: unknown[][] } }): string[] {
  return spy.mock.calls.map((call: unknown[]) => {
    const filter = call[0] as { queryKey?: readonly unknown[] } | undefined;
    return String(filter?.queryKey?.[0] ?? '');
  });
}

describe('egress writes invalidate the routing caches too', () => {
  it.each([
    ['add', async (m: ReturnType<typeof useEgressMutations>) => m.add(uplink)],
    ['update', async (m: ReturnType<typeof useEgressMutations>) => m.update(7, uplink)],
    ['remove', async (m: ReturnType<typeof useEgressMutations>) => m.remove(7)],
  ])('%s refreshes the rule editor, not only the egress list', async (_name, write) => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', null));
    const { result, invalidated } = harness();

    await act(async () => {
      await write(result.current);
    });

    const roots = invalidatedRoots(invalidated);
    expect(roots).toContain('egresses');
    expect(roots).toContain('routing');
  });

  it('leaves both caches alone when the write failed', async () => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'refused', null));
    const { result, invalidated } = harness();

    await act(async () => {
      await result.current.add(uplink);
    });

    expect(invalidatedRoots(invalidated)).toEqual([]);
  });
});
