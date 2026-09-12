import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { advanceLiveTrafficHistory, useLiveTrafficHistory, type LiveTrafficObservation } from '@/hooks/useLiveTrafficHistory';

const sample = (up: number | undefined, down: number | undefined): LiveTrafficObservation[] => [{ key: '7', counters: { up, down } }];
const empty = (): Parameters<typeof advanceLiveTrafficHistory>[0] => ({ scopeKey: '', sampleAt: 0, baselines: {}, series: {} });
beforeEach(() => { vi.spyOn(Date, 'now').mockReturnValue(1000); });
afterEach(() => { vi.restoreAllMocks(); });

describe('fresh cumulative live traffic history', () => {
  it('ignores pre-opening cache, duplicate timestamps and rerenders, then uses actual elapsed time', () => {
    const { result, rerender } = renderHook(useLiveTrafficHistory, { initialProps: { samples: sample(100, 100), sampleAt: 999 } });
    expect(result.current).toEqual({});
    rerender({ samples: sample(100, 100), sampleAt: 1000 });
    expect(result.current['7'].points).toEqual([{ at: 1000, bytesPerSecond: null }]);
    rerender({ samples: sample(300, 500), sampleAt: 6000 });
    expect(result.current['7'].points.at(-1)?.bytesPerSecond).toBe(120);
    rerender({ samples: sample(999999, 999999), sampleAt: 6000 });
    expect(result.current['7'].points).toHaveLength(2);
    rerender({ samples: sample(600, 700), sampleAt: 16000 });
    expect(result.current['7'].points.at(-1)?.bytesPerSecond).toBe(50);
    expect(result.current['7'].status).toBe('ready');
  });

  it('records zero only when a new successful observation confirms unchanged counters', () => {
    const same = sample(100, 200);
    const { result, rerender } = renderHook(useLiveTrafficHistory, { initialProps: { samples: same, sampleAt: 1000 } });
    act(() => { vi.mocked(Date.now).mockReturnValue(11000); });
    rerender({ samples: same, sampleAt: 1000 });
    expect(result.current['7'].points).toHaveLength(1);
    rerender({ samples: same, sampleAt: 11000 });
    rerender({ samples: same, sampleAt: 21000 });
    expect(result.current['7'].points.map((point) => point.bytesPerSecond)).toEqual([null, 0, 0]);
    expect(result.current['7'].status).toBe('ready');
  });

  it('breaks after failed reads and does not count the recovery snapshot as a burst', () => {
    const { result, rerender } = renderHook(useLiveTrafficHistory, { initialProps: { samples: sample(0, 0), sampleAt: 1000 } });
    rerender({ samples: sample(100, 0), sampleAt: 11000 });
    rerender({ samples: sample(200, 0), sampleAt: 21000 });
    rerender({ samples: sample(200, 0), sampleAt: 0 });
    expect(result.current['7'].status).toBe('gap');
    rerender({ samples: sample(10000, 0), sampleAt: 22000 });
    expect(result.current['7'].points.at(-1)?.bytesPerSecond).toBeNull();
    rerender({ samples: sample(10100, 0), sampleAt: 32000 });
    expect(result.current['7'].points.at(-1)?.bytesPerSecond).toBe(10);
    expect(result.current['7'].status).toBe('waiting');
  });

  it('detects a member counter reset even if the aggregate total rises', () => {
    let state = advanceLiveTrafficHistory(empty(), { samples: [{ key: 'core', counters: { a: 1000, b: 1000 } }], sampleAt: 1000 }, 1000);
    state = advanceLiveTrafficHistory(state, { samples: [{ key: 'core', counters: { a: 10, b: 5000 } }], sampleAt: 11000 }, 1000);
    expect(state.series.core.points.at(-1)?.bytesPerSecond).toBeNull();
    state = advanceLiveTrafficHistory(state, { samples: [{ key: 'core', counters: { a: 110, b: 5100 } }], sampleAt: 21000 }, 1000);
    expect(state.series.core.points.at(-1)?.bytesPerSecond).toBe(20);
  });

  it('starts a new baseline after a long gap and after membership changes', () => {
    let state = advanceLiveTrafficHistory(empty(), { samples: sample(100, 200), sampleAt: 1000 }, 1000);
    state = advanceLiveTrafficHistory(state, { samples: sample(10000, 20000), sampleAt: 40000 }, 1000);
    expect(state.series['7'].points.at(-1)?.bytesPerSecond).toBeNull();
    state = advanceLiveTrafficHistory(state, { samples: [{ key: '7', counters: { replacement: 50000 } }], sampleAt: 50000 }, 1000);
    expect(state.series['7'].points).toEqual([{ at: 50000, bytesPerSecond: null }]);
  });

  it.each([undefined, Number.NaN, -1, Number.POSITIVE_INFINITY])('leaves missing or invalid counter %s unknown', (value) => {
    const state = advanceLiveTrafficHistory(empty(), { samples: sample(value, 100), sampleAt: 1000 }, 1000);
    expect(state.series['7'].points).toEqual([]);
    expect(state.series['7'].status).toBe('gap');
  });

  it('bounds history, removes vanished rows and clears prior scope data', () => {
    let state = empty();
    for (let i = 0; i < 100; i += 1) state = advanceLiveTrafficHistory(state, { samples: sample(i * 100, 0), sampleAt: 1000 + i * 1000 }, 1000);
    expect(state.series['7'].points).toHaveLength(32);
    expect(state.series['7'].points[0].at).toBe(69000);
    state = advanceLiveTrafficHistory(state, { samples: [], sampleAt: 101000 }, 1000);
    expect(state.series).toEqual({});
    state = advanceLiveTrafficHistory(state, { samples: sample(500000, 0), sampleAt: 102000, scopeKey: 'different-user' }, 1000);
    expect(state.series['7'].points).toEqual([{ at: 102000, bytesPerSecond: null }]);
  });
});
