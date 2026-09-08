import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { notification } from 'antd';

import { getSharedWebSocketClient } from '@/api/websocket';
import { keys } from '@/api/queryKeys';
import { isRecentLocalInvalidate } from '@/api/invalidationTracker';
import { pushNotification, type FeedLevel } from '@/api/notificationFeed';
import { translateEvent } from '@/api/eventText';
import { Status } from '@/models/status';
import { StatusSchema } from '@/schemas/status';

type Handler = (payload: unknown) => void;

const NOTIFICATION_LEVELS = new Set(['success', 'info', 'warning', 'error']);

function isParams(value: unknown): value is Record<string, string> {
  return !!value && typeof value === 'object' && Object.values(value).every((v) => typeof v === 'string');
}

let invalidateTimer: number | null = null;

export function useWebSocketBridge() {
  const queryClient = useQueryClient();

  useEffect(() => {
    const client = getSharedWebSocketClient();

    const onInvalidate: Handler = (payload) => {
      const p = payload as { type?: string } | undefined;
      if (!p || (p.type !== 'inbounds' && p.type !== 'clients')) return;
      if (invalidateTimer != null) clearTimeout(invalidateTimer);
      invalidateTimer = window.setTimeout(() => {
        invalidateTimer = null;
        if (isRecentLocalInvalidate()) return;
        if (p.type === 'inbounds') {
          queryClient.invalidateQueries({ queryKey: ['inbounds'] });
        } else {
          queryClient.invalidateQueries({ queryKey: ['clients'] });
        }
      }, 200);
    };

    const onOutbounds: Handler = (payload) => {
      if (!Array.isArray(payload)) return;
      queryClient.setQueryData(keys.xray.outboundsTraffic(), payload);
    };

    const onNodes: Handler = (payload) => {
      if (!Array.isArray(payload)) return;
      queryClient.setQueryData(keys.nodes.list(), payload);
    };

    const onInbounds: Handler = (payload) => {
      if (!Array.isArray(payload)) return;
      queryClient.setQueryData(keys.inbounds.slim(), payload);
    };

    /* The 2s telemetry push feeds the same cache the REST poll fills, so the */
    /* dashboard follows the socket and the poll becomes a fallback. F345. */
    const onStatus: Handler = (payload) => {
      const parsed = StatusSchema.safeParse(payload);
      if (!parsed.success) return;
      queryClient.setQueryData(keys.server.status(), new Status(parsed.data));
    };

    const onNotification: Handler = (payload) => {
      const p = payload as {
        title?: unknown; message?: unknown; level?: unknown;
        titleKey?: unknown; messageKey?: unknown; params?: unknown;
      } | undefined;
      if (!p || typeof p.title !== 'string') return;
      const level = typeof p.level === 'string' && NOTIFICATION_LEVELS.has(p.level) ? p.level : 'info';
      const message = typeof p.message === 'string' ? p.message : undefined;
      const titleKey = typeof p.titleKey === 'string' ? p.titleKey : undefined;
      const messageKey = typeof p.messageKey === 'string' ? p.messageKey : undefined;
      const params = isParams(p.params) ? p.params : undefined;
      /* The toast renders now, so it resolves the key here; the feed keeps the key
         so a language change re-renders the history correctly. */
      notification.open({
        type: level as FeedLevel,
        message: titleKey ? translateEvent(titleKey, params) : p.title,
        description: messageKey ? translateEvent(messageKey, params) : message,
      });
      pushNotification({ level: level as FeedLevel, title: p.title, message, titleKey, messageKey, params });
    };

    const onXrayState: Handler = (payload) => {
      const p = payload as { state?: unknown; errorMsg?: unknown } | undefined;
      if (!p || typeof p.state !== 'string') return;
      queryClient.invalidateQueries({ queryKey: keys.server.status() });
      if (p.state === 'error' && typeof p.errorMsg === 'string' && p.errorMsg !== '') {
        notification.open({ type: 'error', message: 'Xray', description: p.errorMsg });
        pushNotification({ level: 'error', title: 'Xray', message: p.errorMsg });
      }
    };

    client.on('invalidate', onInvalidate);
    client.on('outbounds', onOutbounds);
    client.on('nodes', onNodes);
    client.on('inbounds', onInbounds);
    client.on('status', onStatus);
    client.on('notification', onNotification);
    client.on('xray_state', onXrayState);
    client.connect();

    return () => {
      client.off('invalidate', onInvalidate);
      client.off('outbounds', onOutbounds);
      client.off('nodes', onNodes);
      client.off('inbounds', onInbounds);
      client.off('status', onStatus);
      client.off('notification', onNotification);
      client.off('xray_state', onXrayState);
      if (invalidateTimer != null) {
        clearTimeout(invalidateTimer);
        invalidateTimer = null;
      }
    };
  }, [queryClient]);
}
