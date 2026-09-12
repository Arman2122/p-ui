import { ActivityPageSchema, ActivityViewSchema, type ActivityView } from '@/generated/zod';
import { HttpUtil } from '@/utils';
import { z } from 'zod';

export const activityKeys = { root: ['activity'] as const, feed: ['activity', 'feed'] as const, alerts: ['activity', 'alerts'] as const };

export async function fetchActivity(before = 0, limit = 100) {
  const msg = await HttpUtil.get(`/panel/api/activity?before=${before}&limit=${limit}`, undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'activity.loadFailed');
  return ActivityPageSchema.parse(msg.obj);
}

export async function fetchAlerts(): Promise<ActivityView[]> {
  const msg = await HttpUtil.get('/panel/api/activity/alerts', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'activity.loadFailed');
  return z.array(ActivityViewSchema).parse(msg.obj);
}

export async function saveActivityRead(body: { id?: number; throughId?: number }): Promise<boolean> {
  const msg = await HttpUtil.post('/panel/api/activity/read', body, { headers: { 'Content-Type': 'application/json' }, silentSuccess: true });
  return !!msg?.success;
}
