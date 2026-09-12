import { useEffect, useRef, useState } from 'react';

export interface LiveTrafficObservation {
  key: string;
  counters: Readonly<Record<string, number | undefined>>;
  scopeKey?: string;
}
export interface LiveTrafficPoint { at: number; bytesPerSecond: number | null }
export interface LiveTrafficSeries {
  points: LiveTrafficPoint[];
  observedAt: number;
  status: 'waiting' | 'ready' | 'gap';
}
interface LiveTrafficBaseline { at: number; counters: Record<string, number>; membership: string }
interface LiveTrafficState {
  scopeKey: string;
  sampleAt: number;
  baselines: Record<string, LiveTrafficBaseline>;
  series: Record<string, LiveTrafficSeries>;
}
export interface LiveTrafficHistoryOptions {
  samples: readonly LiveTrafficObservation[];
  sampleAt: number;
  scopeKey?: string;
  maxPoints?: number;
  maxGapMs?: number;
}
const emptyHistory = (scopeKey: string): LiveTrafficState => ({ scopeKey, sampleAt: 0, baselines: {}, series: {} });
const finiteCounter = (value: number | undefined): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0;

export function advanceLiveTrafficHistory(previous: LiveTrafficState, options: LiveTrafficHistoryOptions, openedAt: number): LiveTrafficState {
  const { samples, sampleAt, scopeKey = '' } = options;
  const state = previous.scopeKey === scopeKey ? previous : emptyHistory(scopeKey);
  if (!Number.isFinite(sampleAt) || sampleAt <= 0) {
    if (!Object.keys(state.baselines).length) return state;
    return { ...state, baselines: {}, series: Object.fromEntries(Object.entries(state.series).map(([key, value]) => [key, { ...value, status: 'gap' }])) };
  }
  if (sampleAt < openedAt || sampleAt <= state.sampleAt) return state;
  const maxPoints = Math.min(120, Math.max(3, options.maxPoints ?? 32));
  const maxGapMs = Math.max(1000, options.maxGapMs ?? 30_000);
  const baselines: Record<string, LiveTrafficBaseline> = {};
  const series: Record<string, LiveTrafficSeries> = {};
  for (const sample of samples) {
    const entries = Object.entries(sample.counters).sort(([a], [b]) => a.localeCompare(b));
    const current = state.series[sample.key];
    if (!entries.length || entries.some(([, value]) => !finiteCounter(value))) {
      series[sample.key] = { points: current?.points ?? [], observedAt: sampleAt, status: 'gap' };
      continue;
    }
    const counters = Object.fromEntries(entries) as Record<string, number>;
    const membership = JSON.stringify([sample.scopeKey ?? '', entries.map(([key]) => key)]);
    const previousSample = state.baselines[sample.key];
    const elapsed = previousSample ? sampleAt - previousSample.at : 0;
    const continuous = previousSample && previousSample.membership === membership && elapsed > 0 && elapsed <= maxGapMs
      && entries.every(([key, value]) => value! >= previousSample.counters[key]);
    const rate = continuous ? entries.reduce((sum, [key, value]) => sum + value! - previousSample.counters[key], 0) / (elapsed / 1000) : null;
    const changedScope = previousSample && previousSample.membership !== membership;
    const points = [...(changedScope ? [] : current?.points ?? []), { at: sampleAt, bytesPerSecond: rate }].slice(-maxPoints);
    let lastGap = points.length - 1;
    while (lastGap >= 0 && points[lastGap].bytesPerSecond !== null) lastGap -= 1;
    const ready = rate !== null && points.length - lastGap - 1 >= 2;
    series[sample.key] = { points, observedAt: sampleAt, status: ready ? 'ready' : rate === null && current ? 'gap' : 'waiting' };
    baselines[sample.key] = { at: sampleAt, counters, membership };
  }
  return { scopeKey, sampleAt, baselines, series };
}

export function useLiveTrafficHistory(options: LiveTrafficHistoryOptions): Record<string, LiveTrafficSeries> {
  const { samples, sampleAt, scopeKey, maxPoints, maxGapMs } = options;
  const openedAt = useRef(Date.now());
  const history = useRef<LiveTrafficState>(emptyHistory(options.scopeKey ?? ''));
  const [series, setSeries] = useState<Record<string, LiveTrafficSeries>>({});
  useEffect(() => {
    const next = advanceLiveTrafficHistory(history.current, { samples, sampleAt, scopeKey, maxPoints, maxGapMs }, openedAt.current);
    if (next === history.current) return;
    history.current = next;
    setSeries(next.series);
  }, [samples, sampleAt, scopeKey, maxPoints, maxGapMs]);
  return history.current.scopeKey === (options.scopeKey ?? '') ? series : {};
}
