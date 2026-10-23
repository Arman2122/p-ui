import type { ReactNode } from 'react';
import { render } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';

import { ThemeProvider } from '@/hooks/useTheme';
import NodeList from '@/pages/nodes/NodeList';
import type { NodeRecord } from '@/schemas/node';

import { makeTestQueryClient } from './test-utils';

const trafficChecks = vi.hoisted(() => ({ count: 0 }));

vi.mock('@/pages/nodes/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/pages/nodes/telemetry')>();
  return {
    ...actual,
    nodeTraffic: (...args: Parameters<typeof actual.nodeTraffic>) => {
      trafficChecks.count++;
      return actual.nodeTraffic(...args);
    },
  };
});

// Every heartbeat push re-rendered all rows, unchanged ones too: the columns and
// table props were rebuilt on each render, so every cell re-ran its renderer.
describe('NodeList re-render', () => {
  it('leaves the rows alone when its parent re-renders with the same nodes', () => {
    const queryClient = makeTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>
        <ThemeProvider>{children}</ThemeProvider>
      </QueryClientProvider>
    );
    const nodes: NodeRecord[] = [1, 2, 3].map((id) => ({
      id,
      name: `node-${id}`,
      guid: `g${id}`,
      transitive: false,
      enable: true,
      status: 'online',
      panelVersion: '3.0.0',
    }));
    const noop = () => {};
    const props = {
      nodes,
      isMobile: false,
      latestVersion: '3.0.1',
      selectedIds: [] as number[],
      onSelectionChange: noop,
      onAdd: noop,
      onMtls: noop,
      onEdit: noop,
      onDelete: noop,
      onProbe: noop,
      onToggleEnable: noop,
      onUpdateNode: noop,
      onUpdateSelected: noop,
    };
    const view = render(<NodeList {...props} />, { wrapper });
    expect(trafficChecks.count).toBeGreaterThan(0);

    trafficChecks.count = 0;
    view.rerender(<NodeList {...props} />);

    expect(trafficChecks.count).toBe(0);
    view.rerender(<NodeList {...props} nodes={[{ ...nodes[0], name: 'renamed-node' }, ...nodes.slice(1)]} />);
    expect(view.getByText('renamed-node')).toBeTruthy();
    expect(trafficChecks.count).toBeGreaterThan(0);
  });
});
