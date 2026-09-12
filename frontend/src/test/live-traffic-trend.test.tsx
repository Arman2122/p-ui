import { describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import LiveTrafficTrend from '@/components/viz/LiveTrafficTrend';
import { useInboundColumns } from '@/pages/inbounds/list/useInboundColumns';
import { renderWithProviders } from './test-utils';

const ready = { status: 'ready' as const, observedAt: 41000, points: [
  { at: 1000, bytesPerSecond: 10 }, { at: 11000, bytesPerSecond: 20 }, { at: 21000, bytesPerSecond: null }, { at: 31000, bytesPerSecond: 10 }, { at: 41000, bytesPerSecond: 10 },
] };

describe('live traffic SVG and inbound column', () => {
  it('renders unknown until enough real rate points and shows actual zero when sampled', () => {
    const view = renderWithProviders(<LiveTrafficTrend series={{ status: 'waiting', observedAt: 11000, points: [{ at: 1000, bytesPerSecond: null }, { at: 11000, bytesPerSecond: 10 }] }} />);
    expect(view.container.querySelector('svg')).toBeNull();
    expect(view.container.textContent).toBe('—');
    view.rerender(<LiveTrafficTrend series={{ status: 'ready', observedAt: 21000, points: [{ at: 11000, bytesPerSecond: 0 }, { at: 21000, bytesPerSecond: 0 }] }} />);
    expect(view.container.querySelector('path')?.getAttribute('d')).toBe('M2.00,25.00 L98.00,25.00');
  });

  it('does not connect across unknown intervals and uses actual time spacing', () => {
    const view = renderWithProviders(<LiveTrafficTrend series={ready} />);
    expect(view.container.querySelectorAll('path')).toHaveLength(2);
    expect(view.container.querySelectorAll('path')[0].getAttribute('d')).toBe('M2.00,14.00 L26.00,3.00');
    expect(view.container.querySelectorAll('path')[1].getAttribute('d')).toBe('M74.00,14.00 L98.00,14.00');
  });

  it('places the compact Trend column between Traffic and Speed', () => {
    const { result } = renderHook(() => useInboundColumns({ hasAnyRemark: true, hasAnySubSortIndex: false, hasActiveNode: false,
      nodesById: new Map(), clientCount: {}, inboundSpeed: {}, inboundTrends: { '7': ready }, subEnable: false,
      expireDiff: 0, trafficDiff: 0, onRowAction: vi.fn(), onSwitchEnable: vi.fn() }));
    const keys = result.current.map((column) => column.key);
    expect(keys.slice(keys.indexOf('traffic'), keys.indexOf('speed') + 1)).toEqual(['traffic', 'trend', 'speed']);
    expect(result.current.find((column) => column.key === 'trend')?.width).toBe(104);
  });
});
