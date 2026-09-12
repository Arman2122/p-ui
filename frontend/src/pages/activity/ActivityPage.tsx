import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Space, Tag } from 'antd';
import { fetchActivity } from '@/api/activity';
import { asFeedItem, markRead } from '@/api/notificationFeed';
import { translateEvent } from '@/api/eventText';
import { PageHeader, PanelCard, EventItem } from '@/components/ui/panel';
import { useRelativeTime } from '@/hooks/useRelativeTime';
import { useSignedInUser } from '@/pages/resellers/api';

export default function ActivityPage() {
  const { data: user } = useSignedInUser();
  return <ActivityHistory key={user?.id ?? 'loading'} viewer={user ? `${user.id}:${user.role}` : ''} />;
}

function ActivityHistory({ viewer }: { viewer: string }) {
  const { t, i18n } = useTranslation();
  const relative = useRelativeTime();
  const [cursors, setCursors] = useState([0]);
  const before = cursors[cursors.length - 1];
  const query = useQuery({ queryKey: ['activity', 'history', viewer, before], queryFn: () => fetchActivity(before, 50), enabled: !!viewer });
  const rows = query.data?.items.map(asFeedItem) ?? [];
  return <>
    <PageHeader title={t('activity.history')} sub={t('activity.retention')} actions={<Button loading={query.isFetching} onClick={() => { void query.refetch(); }}>{t('refresh')}</Button>} />
    {query.error && <Alert type="error" showIcon title={t('activity.loadFailed')} description={t(query.error.message)} />}
    <PanelCard pad={14}>
      {rows.map((item, index) => <div key={item.id} className="p-activity-entry">
        <EventItem time={relative(item.at)} title={translateEvent(item.titleKey ?? '', item.params) || item.title}
          meta={translateEvent(item.messageKey ?? '', item.params) || item.message} tone={item.level === 'error' ? 'var(--p-error)' : item.level === 'warning' ? 'var(--p-warning)' : 'var(--p-primary)'}
          icon="history" last={index === rows.length - 1} />
        <Space><Tag>{new Date(item.at).toLocaleString(i18n.language)}</Tag>
          <Button size="small" type="text" disabled={item.read} onClick={() => { void markRead(item.id); }}>{item.read ? t('activity.read') : t('activity.markRead')}</Button></Space>
      </div>)}
      {!rows.length && <p className="p-sub">{query.isPending ? t('loading') : t('menu.nothingHere')}</p>}
    </PanelCard>
    <Space><Button disabled={cursors.length === 1 || query.isFetching} onClick={() => setCursors((v) => v.slice(0, -1))}>{t('activity.newer')}</Button>
      <Button disabled={!query.data?.nextBefore || query.isFetching} onClick={() => setCursors((v) => [...v, query.data?.nextBefore ?? 0])}>{t('activity.older')}</Button></Space>
  </>;
}
