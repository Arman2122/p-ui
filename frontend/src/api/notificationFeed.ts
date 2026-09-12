import { useEffect, useLayoutEffect, useSyncExternalStore } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { ActivityView } from '@/generated/zod';
import { activityKeys, fetchActivity, fetchAlerts, saveActivityRead } from '@/api/activity';

export type FeedLevel = 'success' | 'info' | 'warning' | 'error';

export type FeedItem = {
  id: string;
  level: FeedLevel;
  title: string;
  message: string;
  at: number;
  read: boolean;
  /* Server-sent events name an i18n key and its parameters so the browser, which
     is the side that knows the locale, renders them. Absent for local pushes. */
  titleKey?: string;
  messageKey?: string;
  params?: Record<string, string>;
  category?: 'activity' | 'alert';
};

/* A warning or an error is something to act on; the rest is history. The bell's
   two tabs are that split, not a second classification the server has to send. */
export function isAlert(item: FeedItem): boolean {
  if (item.category) return item.category === 'alert';
  return item.level === 'warning' || item.level === 'error';
}

const MAX_ITEMS = 200;

let items: FeedItem[] = [];
let seq = 0;
const listeners = new Set<() => void>();
let refreshRemote: (() => Promise<unknown>) | undefined;
let state = { ready: false, loading: false, error: '', unread: 0, recent: 0 };
const readIDs = new Set<string>();
let readThrough = 0;
let generation = '';
let feedEpoch = 0;

export function asFeedItem(item: ActivityView): FeedItem {
  const level = ['success', 'info', 'warning', 'error'].includes(item.level) ? item.level as FeedLevel : 'info';
  return { ...item, level, category: item.category === 'alert' ? 'alert' : 'activity' };
}

export function useActivityBridge(viewer = 'session') {
  const client = useQueryClient();
  const feed = useQuery({ queryKey: [...activityKeys.feed, viewer], queryFn: () => fetchActivity(), refetchInterval: 15_000, retry: 1 });
  const alerts = useQuery({ queryKey: [...activityKeys.alerts, viewer], queryFn: fetchAlerts, refetchInterval: 30_000, retry: 1 });
  useLayoutEffect(() => { clearNotifications(); }, [viewer]);
  useEffect(() => {
    refreshRemote = () => client.invalidateQueries({ queryKey: activityKeys.root });
    return () => { refreshRemote = undefined; };
  }, [client]);
  useEffect(() => {
    if (feed.data) {
      if (generation !== feed.data.generation) {
        feedEpoch++;
        items = []; readIDs.clear(); readThrough = 0; generation = feed.data.generation;
      }
      const newest = Number(feed.data.items[0]?.id ?? 0);
      const retained = items.filter((item) => item.id.startsWith('n') || (item.category === 'activity' && Number(item.id) > newest));
      const saved = feed.data.items.map(asFeedItem).map((item) => ({ ...item, read: item.read || readIDs.has(item.id) || Number(item.id) <= readThrough }));
      for (const item of feed.data.items) { if (item.read) readIDs.delete(item.id); }
      const active = (alerts.data ?? []).map(asFeedItem);
      items = [...active, ...retained, ...saved].filter((item, i, all) => all.findIndex((other) => other.id === item.id) === i).slice(0, MAX_ITEMS);
    }
    state = { ready: !!feed.data, loading: feed.isPending, error: feed.error?.message || alerts.error?.message || '',
      unread: feed.data?.unread ?? 0, recent: feed.data?.recent ?? 0 };
    emit();
  }, [feed.data, feed.isPending, feed.error, alerts.data, alerts.error]);
}

export function useNotificationState() { return useSyncExternalStore(subscribe, () => state, () => state); }

function emit() {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function snapshot() {
  return items;
}

export function pushNotification(input: {
  level: FeedLevel;
  title: string;
  message?: string;
  titleKey?: string;
  messageKey?: string;
  params?: Record<string, string>;
}): void {
  seq += 1;
  const item: FeedItem = {
    id: `n${seq}`,
    level: input.level,
    title: input.title,
    message: input.message ?? '',
    at: Date.now(),
    read: false,
    titleKey: input.titleKey,
    messageKey: input.messageKey,
    params: input.params,
  };
  items = [item, ...items].slice(0, MAX_ITEMS);
  emit();
}

export async function markRead(id: string): Promise<void> {
  if (id.startsWith('alert:')) return;
  const epoch = feedEpoch;
  if (/^\d+$/.test(id)) {
    if (!(await saveActivityRead({ id: Number(id) }))) return;
    if (epoch !== feedEpoch) return;
    readIDs.add(id);
  }
  let changed = false;
  items = items.map((item) => {
    if (item.id !== id || item.read) return item;
    changed = true;
    return { ...item, read: true };
  });
  if (changed) emit();
  if (refreshRemote) await refreshRemote();
}

export async function markAllRead(): Promise<void> {
  if (!state.unread && !items.some((item) => !item.read)) return;
  const epoch = feedEpoch;
  const throughId = Math.max(0, ...items.filter((item) => /^\d+$/.test(item.id)).map((item) => Number(item.id)));
  if (throughId > 0) {
    if (!(await saveActivityRead({ throughId }))) return;
    if (epoch !== feedEpoch) return;
    readThrough = Math.max(readThrough, throughId);
  }
  items = items.map((item) => (item.read ? item : { ...item, read: true }));
  emit();
  if (refreshRemote) await refreshRemote();
}

export function clearNotifications(): void {
  feedEpoch++;
  items = [];
  readIDs.clear();
  readThrough = 0;
  generation = '';
  state = { ready: false, loading: false, error: '', unread: 0, recent: 0 };
  emit();
}

export function useNotificationFeed(): FeedItem[] {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
