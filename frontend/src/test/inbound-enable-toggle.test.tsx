import type { ReactNode } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';

import InboundList from '@/pages/inbounds/list/InboundList';
import type { DBInboundRecord, InboundListProps } from '@/pages/inbounds/list/types';
import { makeTestQueryClient } from '@/test/test-utils';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => {
  vi.restoreAllMocks();
});

/*
The enable switch used to write onto the row object in place.

Those rows come out of the query cache, and the switch is fully controlled by
`record.enable`. Assigning to it changes no reference, so React renders nothing;
and TanStack's structural sharing then finds the refetched row already equal to
the mutated one and keeps the old reference, skipping the very update that would
have repainted the switch. The operator clicked, the request succeeded, and the
switch stayed where it was until something unrelated forced a render.

The rollback had the same problem, which is what the finding named: on a refusal
the row was written back and, again, nothing re-rendered.

So the row is no longer touched at all -- the write is followed by an
invalidation, and the list follows the server's answer, as every other list on
the panel does. F181.
*/

const ROW: DBInboundRecord = {
  id: 7,
  enable: true,
  remark: 'edge',
  subSortIndex: 1,
  port: 443,
  protocol: 'vless',
  up: 0,
  down: 0,
  total: 0,
  expiryTime: 0,
  _expiryTime: null,
  settings: '{}',
  streamSettings: '{}',
};

function harness(rows: DBInboundRecord[]) {
  const queryClient = makeTestQueryClient();
  const invalidated = vi.spyOn(queryClient, 'invalidateQueries');
  const props: InboundListProps = {
    dbInbounds: rows,
    clientCount: {},
    onlineClients: [],
    lastOnlineMap: {},
    inboundSpeed: {},
    expireDiff: 0,
    trafficDiff: 0,
    pageSize: 10,
    isMobile: false,
    subEnable: false,
    nodesById: new Map(),
    hasActiveNode: false,
    onAddInbound: () => {},
    onGeneralAction: () => {},
    onRowAction: () => {},
    onBulkDelete: async () => true,
  };
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  render(<InboundList {...props} />, { wrapper });
  return { invalidated };
}

async function clickTheEnableSwitch() {
  const toggle = await screen.findByRole('switch');
  await userEvent.click(toggle);
}

describe('the inbound enable switch', () => {
  it('leaves the cached row alone and asks for a refresh', async () => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, '', null));
    const rows = [{ ...ROW }];
    const { invalidated } = harness(rows);

    await clickTheEnableSwitch();

    await waitFor(() => expect(invalidated).toHaveBeenCalled());
    const roots = invalidated.mock.calls.map(
      (call) => String((call[0] as { queryKey?: readonly unknown[] } | undefined)?.queryKey?.[0] ?? ''),
    );
    expect(roots, 'nothing asked for the inbound list again, so the switch waits on a websocket push that a lone panel may never send').toContain('inbounds');
    expect(
      rows[0].enable,
      'the row handed in was written to in place; it lives in the query cache, where a mutation renders nothing and then defeats the structural-sharing check that would have',
    ).toBe(true);
  });

  it('asks for nothing when the panel refused the change', async () => {
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'refused', null));
    const rows = [{ ...ROW }];
    const { invalidated } = harness(rows);

    await clickTheEnableSwitch();

    await waitFor(() => expect(HttpUtil.post).toHaveBeenCalled());
    expect(invalidated).not.toHaveBeenCalled();
    expect(rows[0].enable, 'a refused change still altered the row').toBe(true);
  });
});
