import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Tag, Tooltip } from 'antd';

import { Sparkline } from '@/components/viz';
import { Icon } from '@/components/ui/Icon';
import { EventItem, Field, Kicker, PanelCard } from '@/components/ui/panel';
import { SizeFormatter, TimeFormatter } from '@/utils';
import { activateOnKey } from '@/utils/a11y';
import { isAlert, useNotificationFeed, type FeedItem } from '@/api/notificationFeed';
import { translateEvent } from '@/api/eventText';
import type { Status } from '@/models/status';
import { mean, peak, type OverviewHistory } from './useOverviewHistory';

const UP_TONE = '#4c8dff';
const DOWN_TONE = '#5ac8b0';
const UDP_TONE = '#8c8f99';

function Legend({ tone, label, value }: { tone: string; label: string; value: string }) {
  return (
    <span className="p-legend">
      <i style={{ background: tone }} />
      {label}
      <b>{value}</b>
    </span>
  );
}

export function ThroughputPanel({ status, history }: { status: Status; history: OverviewHistory }) {
  const { t } = useTranslation();
  const up = history.series.netUp;
  const down = history.series.netDown;

  /* A reference line does not widen the y-domain, so these are only safe because
     the current rate is by definition the last point in the series. */
  const referenceLines = useMemo(
    () => [
      { y: status.netIO.down, color: DOWN_TONE, dash: '2 4' },
      { y: status.netIO.up, color: UP_TONE, dash: '2 4' },
    ],
    [status.netIO.up, status.netIO.down],
  );

  const cells: [string, string, string][] = [
    [t('pages.index.sent'), SizeFormatter.sizeFormat(status.netTraffic.sent), t('pages.index.throughputSub')],
    [t('pages.index.received'), SizeFormatter.sizeFormat(status.netTraffic.recv), t('pages.index.throughputSub')],
    [t('pages.index.avgWindow'), `↑ ${SizeFormatter.speedFormat(mean(up))}`, `↓ ${SizeFormatter.speedFormat(mean(down))}`],
    [t('pages.index.peak'), SizeFormatter.speedFormat(Math.max(peak(up), peak(down))), t('pages.index.throughputSub')],
  ];

  return (
    <PanelCard
      kicker={t('pages.index.overallSpeed')}
      icon="chart"
      pad={0}
      className="p-span2"
      extra={
        <>
          <Legend tone={UP_TONE} label={t('pages.index.upload')} value={SizeFormatter.speedFormat(status.netIO.up)} />
          <Legend tone={DOWN_TONE} label={t('pages.index.download')} value={SizeFormatter.speedFormat(status.netIO.down)} />
        </>
      }
    >
      <div style={{ padding: '10px 6px 0' }}>
        <Sparkline
          data={up}
          data2={down}
          labels={history.labels}
          stroke={UP_TONE}
          stroke2={DOWN_TONE}
          name1={t('pages.index.upload')}
          name2={t('pages.index.download')}
          height={176}
          strokeWidth={1.7}
          fillOpacity={0.1}
          showAxes
          showLegend={false}
          valueMin={0}
          valueMax={null}
          yFormatter={SizeFormatter.speedFormat}
          referenceLines={referenceLines}
        />
      </div>
      <div className="p-kvgrid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(120px,1fr))', borderTop: '1px solid var(--p-border)' }}>
        {cells.map(([label, value, sub]) => (
          <div key={label} style={{ padding: '11px 14px' }}>
            <Field label={label} value={value} sub={sub} />
          </div>
        ))}
      </div>
    </PanelCard>
  );
}

export function SessionsPanel({ status, history }: { status: Status; history: OverviewHistory }) {
  const { t } = useTranslation();
  const total = status.tcpCount + status.udpCount;
  const tcpShare = total > 0 ? (status.tcpCount / total) * 100 : 0;
  const udpShare = total > 0 ? (status.udpCount / total) * 100 : 0;

  return (
    <PanelCard kicker={t('pages.index.liveSessions')} icon="hosts" pad={0}>
      <div style={{ padding: '14px 14px 0' }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 4 }}>
          <span className="p-stat">{total}</span>
          <span className="p-stat-u">{t('pages.index.sockets')}</span>
        </div>
        <div style={{ marginTop: 12, display: 'flex', height: 6, borderRadius: 99, overflow: 'hidden', background: 'var(--p-fill)' }}>
          <i style={{ width: `${tcpShare}%`, background: UP_TONE }} />
          <i style={{ width: `${udpShare}%`, background: UDP_TONE }} />
        </div>
        <div style={{ display: 'flex', gap: 16, marginTop: 8 }}>
          <Legend tone={UP_TONE} label={t('pages.index.tcp')} value={String(status.tcpCount)} />
          <Legend tone={UDP_TONE} label={t('pages.index.udp')} value={String(status.udpCount)} />
        </div>
      </div>
      <div style={{ padding: '12px 0 0' }}>
        <Sparkline
          data={history.series.tcpCount}
          data2={history.series.udpCount}
          labels={history.labels}
          height={96}
          stroke={UP_TONE}
          stroke2={UDP_TONE}
          name1={t('pages.index.tcp')}
          name2={t('pages.index.udp')}
          strokeWidth={1.6}
          fillOpacity={0.12}
          showLegend={false}
          valueMin={0}
          valueMax={null}
          yFormatter={(v) => String(Math.round(v))}
        />
      </div>
    </PanelCard>
  );
}

function toneOf(item: FeedItem): string {
  if (item.level === 'error') return 'var(--p-error)';
  if (item.level === 'warning') return 'var(--p-warning)';
  if (item.level === 'success') return 'var(--p-success)';
  return 'var(--p-primary)';
}

export function ActivityPanel({ relative }: { relative: (at: number) => string }) {
  const { t } = useTranslation();
  const feed = useNotificationFeed();
  const items = feed.slice(0, 8);

  return (
    <PanelCard
      kicker={t('pages.index.activity')}
      icon="history"
      pad={0}
    >
      <div style={{ padding: 14 }}>
        {items.map((item, i) => (
          <EventItem
            key={item.id}
            time={relative(item.at)}
            title={item.titleKey ? translateEvent(item.titleKey, item.params) : item.title}
            meta={item.messageKey ? translateEvent(item.messageKey, item.params) : item.message}
            tone={toneOf(item)}
            icon={isAlert(item) ? 'shield' : 'bell'}
            last={i === items.length - 1}
          />
        ))}
        {!items.length && <div className="p-empty" style={{ padding: 24 }}>{t('pages.index.noActivity')}</div>}
      </div>
    </PanelCard>
  );
}

export function PanelHostPanel({ status, nodeCount, webPort, basePath, sessionMinutes, showIp, onToggleIp }: {
  status: Status;
  nodeCount: number;
  webPort: number;
  basePath: string;
  sessionMinutes: number;
  showIp: boolean;
  onToggleIp: () => void;
}) {
  const { t } = useTranslation();
  const ips = [status.publicIP.ipv4, status.publicIP.ipv6]
    .map((v) => String(v))
    .filter((v) => v && v !== '0' && v !== 'N/A');

  const cells: [string, string][] = [
    [t('pages.index.memory'), SizeFormatter.sizeFormat(status.appStats.mem)],
    [t('pages.index.threads'), String(status.appStats.threads)],
    [t('pages.index.listenPort'), String(webPort)],
    [t('pages.index.nodesLinked'), String(nodeCount)],
    [t('pages.index.uptime'), TimeFormatter.formatSecond(status.uptime)],
    ['Xray', status.xray.version || '—'],
  ];

  return (
    <PanelCard
      kicker={t('pages.index.panel')}
      icon="settings"
      pad={0}
      extra={
        <Tooltip title={t('pages.index.toggleIpVisibility')}>
          <Button
            type="text"
            size="small"
            aria-label={t('pages.index.toggleIpVisibility')}
            onClick={onToggleIp}
            onKeyDown={activateOnKey(onToggleIp)}
            icon={<Icon name={showIp ? 'eyeOff' : 'eye'} size={15} />}
          />
        </Tooltip>
      }
    >
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr' }}>
        {cells.map(([label, value], i) => (
          <div
            key={label}
            style={{
              padding: '11px 14px',
              borderLeft: i % 2 ? '1px solid var(--p-border)' : undefined,
              borderTop: i > 1 ? '1px solid var(--p-border)' : undefined,
            }}
          >
            <Field label={label} value={value} />
          </div>
        ))}
      </div>
      <div style={{ borderTop: '1px solid var(--p-border)', padding: 14, display: 'flex', flexDirection: 'column', gap: 8 }}>
        <Kicker>{t('pages.index.publicIp')}</Kicker>
        {ips.map((ip) => (
          <div key={ip} style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'center' }}>
            <span className={`p-mono${showIp ? '' : ' ip-hidden'}`} style={{ color: 'var(--p-text)' }}>{ip}</span>
            <Tag style={{ marginInlineEnd: 0 }}>{ip.includes(':') ? 'IPv6' : 'IPv4'}</Tag>
          </div>
        ))}
        {!ips.length && <span className="p-sub">—</span>}
        <div className="p-sub">
          {t('pages.index.basePath')} <span className="p-mono">{basePath}</span>
          {' · '}
          {t('pages.index.sessionLength')} {t('pages.index.minutesShort', { count: sessionMinutes })}
        </div>
      </div>
    </PanelCard>
  );
}
