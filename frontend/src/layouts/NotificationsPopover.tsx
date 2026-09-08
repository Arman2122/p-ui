import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge, Button, Popover, Segmented } from 'antd';

import { Icon, type IconName } from '@/components/ui/Icon';
import { isAlert, markAllRead, markRead, useNotificationFeed, type FeedItem } from '@/api/notificationFeed';
import { translateEvent } from '@/api/eventText';

const DAY_MS = 86_400_000;

function toneOf(item: FeedItem): string {
  if (item.level === 'error') return 'var(--p-error)';
  if (item.level === 'warning') return 'var(--p-warning)';
  if (item.level === 'success') return 'var(--p-success)';
  return 'var(--p-primary)';
}

function iconOf(item: FeedItem): IconName {
  if (item.level === 'error') return 'shield';
  if (item.level === 'warning') return 'clock';
  if (item.level === 'success') return 'check';
  return 'bell';
}

function useRelativeTime() {
  const { i18n } = useTranslation();
  return useMemo(() => {
    const rtf = new Intl.RelativeTimeFormat(i18n.language || 'en', { numeric: 'auto' });
    return (at: number) => {
      const seconds = Math.round((at - Date.now()) / 1000);
      const abs = Math.abs(seconds);
      if (abs < 60) return rtf.format(Math.trunc(seconds), 'second');
      if (abs < 3600) return rtf.format(Math.trunc(seconds / 60), 'minute');
      if (abs < 86400) return rtf.format(Math.trunc(seconds / 3600), 'hour');
      return rtf.format(Math.trunc(seconds / 86400), 'day');
    };
  }, [i18n.language]);
}

function NotificationPanel({ onOpenDashboard }: { onOpenDashboard: () => void }) {
  const { t } = useTranslation();
  const feed = useNotificationFeed();
  const relative = useRelativeTime();
  const [tab, setTab] = useState<'all' | 'alerts' | 'activity'>('all');

  const items = useMemo(() => {
    if (tab === 'alerts') return feed.filter(isAlert);
    if (tab === 'activity') return feed.filter((item) => !isAlert(item));
    return feed;
  }, [feed, tab]);

  const unread = feed.filter((item) => !item.read).length;
  const recent = feed.filter((item) => Date.now() - item.at < DAY_MS).length;

  return (
    <div className="p-pop">
      <div className="p-pop-h">
        <div>
          <div style={{ fontSize: 13, fontWeight: 650, color: 'var(--p-text)' }}>{t('menu.notifications')}</div>
          <div className="p-sub" style={{ marginTop: 1 }}>
            {unread ? t('menu.unreadCount', { count: unread }) : t('menu.allCaughtUp')}
          </div>
        </div>
        <Button size="small" type="text" disabled={!unread} onClick={markAllRead}>{t('menu.markAllRead')}</Button>
      </div>
      <div style={{ padding: '9px 13px', borderBottom: '1px solid var(--p-border)' }}>
        <Segmented
          size="small"
          block
          value={tab}
          onChange={(value) => setTab(value as 'all' | 'alerts' | 'activity')}
          options={[
            { value: 'all', label: t('menu.tabAll') },
            { value: 'alerts', label: t('menu.tabAlerts') },
            { value: 'activity', label: t('menu.tabActivity') },
          ]}
        />
      </div>
      <div className="p-pop-b">
        {items.map((item) => {
          const tone = toneOf(item);
          const body = item.messageKey ? translateEvent(item.messageKey, item.params) : item.message;
          return (
            <button
              type="button"
              key={item.id}
              className={`p-poprow${item.read ? '' : ' is-unread'}`}
              onClick={() => markRead(item.id)}
            >
              <span className="p-alert-i" style={{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }}>
                <Icon name={iconOf(item)} size={13} />
              </span>
              <span style={{ flex: 1, minWidth: 0 }}>
                <span style={{ display: 'flex', alignItems: 'baseline', gap: 7 }}>
                  <span style={{ fontSize: 12.5, fontWeight: item.read ? 500 : 650, color: 'var(--p-text)', whiteSpace: 'normal' }}>
                    {item.titleKey ? translateEvent(item.titleKey, item.params) : item.title}
                  </span>
                </span>
                <span className="p-sub" style={{ display: 'block', marginTop: 2, whiteSpace: 'normal' }}>
                  {body ? `${body} · ` : ''}{relative(item.at)}
                </span>
              </span>
              {!item.read && <span className="p-unread" />}
            </button>
          );
        })}
        {!items.length && <div className="p-empty" style={{ padding: 26 }}>{t('menu.nothingHere')}</div>}
      </div>
      <div className="p-pop-f">
        <span className="p-sub">{t('menu.inLast24h', { count: recent })}</span>
        <Button size="small" onClick={onOpenDashboard}>{t('menu.openDashboard')}</Button>
      </div>
    </div>
  );
}

export default function NotificationsPopover({ onOpenDashboard }: { onOpenDashboard: () => void }) {
  const { t } = useTranslation();
  const feed = useNotificationFeed();
  const unread = feed.filter((item) => !item.read).length;
  return (
    <Popover
      content={<NotificationPanel onOpenDashboard={onOpenDashboard} />}
      trigger="click"
      placement="bottomRight"
      arrow={false}
    >
      <Badge count={unread} size="small" offset={[-3, 4]}>
        <button type="button" className="p-iconbtn" title={t('menu.notifications')} aria-label={t('menu.notifications')}>
          <Icon name="bell" />
        </button>
      </Badge>
    </Popover>
  );
}
