import { useEffect, useRef } from 'react';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

type ChartSeries = { name: string; values: number[]; color: string };

export default function HistoryChart({ times, series, height = 140, format, compact = false }: {
  times: number[];
  series: ChartSeries[];
  height?: number;
  format: (value: number) => string;
  compact?: boolean;
}) {
  const container = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = container.current;
    if (!element || !times.length) return;
    const plot = new uPlot({
      width: Math.max(element.clientWidth, compact ? 1 : 120), height,
      legend: { show: !compact },
      cursor: { show: !compact },
      scales: { x: { time: true }, y: { range: (_u, _min, max) => [0, Math.max(1, max * 1.1)] } },
      axes: [{ show: !compact, stroke: '#8c8f99', grid: { show: false } },
        { show: !compact, stroke: '#8c8f99', size: 84, font: '11px system-ui', values: (_u, ticks) => ticks.map(format) }],
      series: [{}, ...series.map((s) => ({ label: s.name, stroke: s.color, width: 1.6, spanGaps: false,
        value: (_u: uPlot, value: number | null) => value == null ? '—' : format(value) }))],
    }, [times, ...series.map((s) => s.values.map((v) => Number.isFinite(v) ? v : null))], element);
    const resize = new ResizeObserver(() => plot.setSize({ width: Math.max(element.clientWidth, compact ? 1 : 120), height }));
    resize.observe(element);
    return () => { resize.disconnect(); plot.destroy(); };
  }, [times, series, height, format, compact]);
  return <div ref={container} style={{ minWidth: 0, width: '100%', minHeight: height }} />;
}
