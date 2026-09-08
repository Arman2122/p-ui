import { useSyncExternalStore } from 'react';

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
};

/* A warning or an error is something to act on; the rest is history. The bell's
   two tabs are that split, not a second classification the server has to send. */
export function isAlert(item: FeedItem): boolean {
  return item.level === 'warning' || item.level === 'error';
}

const MAX_ITEMS = 200;

let items: FeedItem[] = [];
let seq = 0;
const listeners = new Set<() => void>();

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

export function markRead(id: string): void {
  let changed = false;
  items = items.map((item) => {
    if (item.id !== id || item.read) return item;
    changed = true;
    return { ...item, read: true };
  });
  if (changed) emit();
}

export function markAllRead(): void {
  if (!items.some((item) => !item.read)) return;
  items = items.map((item) => (item.read ? item : { ...item, read: true }));
  emit();
}

export function clearNotifications(): void {
  if (!items.length) return;
  items = [];
  emit();
}

export function useNotificationFeed(): FeedItem[] {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
