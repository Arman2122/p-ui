import { useMemo } from 'react';
import { Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import { SizeFormatter } from '@/utils';
import type { LiveTrafficSeries } from '@/hooks/useLiveTrafficHistory';

interface LiveTrafficTrendProps {
  series?: LiveTrafficSeries;
  height?: number;
  width?: number;
  color?: string;
  showCaption?: boolean;
}

export default function LiveTrafficTrend({ series, height = 28, width, color = '#5ac8b0', showCaption = false }: LiveTrafficTrendProps) {
  const { t } = useTranslation();
  const paths = useMemo(() => {
    const points = series?.points ?? [];
    const valid = points.filter((point) => point.bytesPerSecond !== null);
    if (valid.length < 2) return [];
    const start = points[0].at, span = Math.max(1, points[points.length - 1].at - start);
    const max = Math.max(1, ...valid.map((point) => point.bytesPerSecond!));
    const segments: string[] = [];
    let segment = '', count = 0;
    for (const point of points) {
      if (point.bytesPerSecond === null) { if (count > 1) segments.push(segment); segment = ''; count = 0; continue; }
      const x = 2 + (point.at - start) / span * 96;
      const y = height - 3 - point.bytesPerSecond / max * (height - 6);
      segment += `${count ? ' L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`;
      count += 1;
    }
    if (count > 1) segments.push(segment);
    return segments;
  }, [series, height]);
  const caption = t('liveTraffic.sinceOpening');
  const last = series?.points.at(-1);
  const observed = series?.status === 'ready' && last?.bytesPerSecond != null;
  const reading = observed ? `${SizeFormatter.sizeFormat(last.bytesPerSecond!)}/s` : '—';
  const detail = observed ? `${caption} · ${reading}` : `${caption} · ${t('liveTraffic.waiting')}`;
  return <Tooltip title={<div>{detail}{series?.status === 'gap' && <div>{t('liveTraffic.gap')}</div>}</div>}>
    <div className="live-traffic-trend" style={{ width: width ?? '100%', minWidth: 0 }}>
      <div role="img" aria-label={detail} style={{ height, display: 'flex', alignItems: 'center', color: 'var(--ant-color-text-secondary)' }}>
        {observed && paths.length ? <svg viewBox={`0 0 100 ${height}`} width="100%" height={height} preserveAspectRatio="none" aria-hidden="true">
          {paths.map((path, index) => <path key={index} d={path} fill="none" stroke={color} strokeWidth={1.4} vectorEffect="non-scaling-stroke" strokeLinecap="round" strokeLinejoin="round" />)}
        </svg> : <span>—</span>}
      </div>
      {showCaption && <div className="p-sub" style={{ fontSize: 11, marginTop: 3 }}>{caption}</div>}
    </div>
  </Tooltip>;
}
