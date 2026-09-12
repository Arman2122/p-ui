import { useTranslation } from 'react-i18next';
import { Button, Tag, Tooltip } from 'antd';

import { Icon } from '@/components/ui/Icon';
import { EventItem, Field, PanelCard } from '@/components/ui/panel';
import { SizeFormatter, TimeFormatter } from '@/utils';
import { isAlert, useNotificationFeed, useNotificationState, type FeedItem } from '@/api/notificationFeed';
import { translateEvent } from '@/api/eventText';
import type { Status } from '@/models/status';
export { ThroughputPanel, SessionsPanel } from './DashboardTelemetry';

function toneOf(item: FeedItem): string {
  if (item.level === 'error') return 'var(--p-error)';
  if (item.level === 'warning') return 'var(--p-warning)';
  if (item.level === 'success') return 'var(--p-success)';
  return 'var(--p-primary)';
}

export function ActivityPanel({ relative }: { relative: (at: number) => string }) {
  const { t } = useTranslation();
  const feed = useNotificationFeed();
  const state = useNotificationState();
  const items = feed.filter((item) => item.category !== 'alert').slice(0, 6);

  return (
    <PanelCard
      kicker={t('pages.index.activity')}
      icon="history"
      pad={0}
      className="ov-activity-panel"
      extra={<Button type="text" size="small" href={`${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/panel/activity`}>{t('activity.history')}</Button>}
    >
      <div className="ov-activity-feed">
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
        {state.error && <div role="alert" className="p-sub">{t('activity.loadFailed')}</div>}
        {!items.length && !state.error && <div className="p-empty" style={{ padding: 24 }}>{state.loading ? t('loading') : t('pages.index.noActivity')}</div>}
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
    [t('dashboardRepair.panelRam'), SizeFormatter.sizeFormat(status.appStats.mem)],
    [t('pages.index.threads'), String(status.appStats.threads)],
    [t('pages.index.listenPort'), String(status.listenPort ?? webPort)],
    [t('pages.index.nodesLinked'), String(nodeCount)],
  ];

  return (
    <PanelCard
      kicker={t('dashboardRepair.panelHost')}
      icon="settings"
      pad={0}
      className="ov-host-panel"
      extra={
        <Tooltip title={t('pages.index.toggleIpVisibility')}>
          <Button
            type="text"
            size="small"
            aria-label={t('pages.index.toggleIpVisibility')}
            onClick={onToggleIp}
            icon={<Icon name={showIp ? 'eyeOff' : 'eye'} size={15} />}
          />
        </Tooltip>
      }
    >
      <div className="ov-host-grid">
        {cells.map(([label, value]) => (
          <div key={label} className="ov-host-cell">
            <Field label={label} value={value} />
          </div>
        ))}
      </div>
      <div className="ov-host-addresses" role="group" aria-label={t('pages.index.publicIp')}>
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
        <div className="p-sub ov-host-runtime">{t('pages.index.uptime')} {TimeFormatter.formatSecond(status.uptime)} · Xray {status.xray.version || '—'}</div>
      </div>
    </PanelCard>
  );
}
