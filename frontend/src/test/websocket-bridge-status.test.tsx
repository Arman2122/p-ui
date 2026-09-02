import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { notification } from 'antd';
import type { ReactNode } from 'react';

import { getSharedWebSocketClient } from '@/api/websocket';
import { useWebSocketBridge } from '@/api/websocketBridge';
import { keys } from '@/api/queryKeys';
import { Status } from '@/models/status';

type ListenerMap = { listeners: Map<string, Set<(payload: unknown) => void>> };

/*
The hub's status, notification and xray_state messages reach the page.

The panel pushed a full status snapshot every 2 seconds, a toast on every
Xray stop and restart, and a state change on every Xray transition, and the
bridge subscribed to none of them: the dashboard polled REST for the same
snapshot, and the toasts never appeared. F342, F345.
*/
describe('websocket bridge status, notification and xray_state handlers', () => {
  let originalWS: typeof WebSocket;

  beforeEach(() => {
    originalWS = globalThis.WebSocket;
    class FakeWebSocket {
      static readonly CONNECTING = 0;
      static readonly OPEN = 1;
      static readonly CLOSING = 2;
      static readonly CLOSED = 3;
      readyState = FakeWebSocket.CONNECTING;
      addEventListener() {}
      removeEventListener() {}
      close() {}
      send() {}
    }
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });

  afterEach(() => {
    globalThis.WebSocket = originalWS;
    vi.restoreAllMocks();
  });

  function mount() {
    const queryClient = new QueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    renderHook(() => useWebSocketBridge(), { wrapper });
    const client = getSharedWebSocketClient() as unknown as ListenerMap;
    const fire = (type: string, payload: unknown) => {
      const handlers = client.listeners.get(type);
      expect(handlers, `no bridge handler for '${type}'`).toBeDefined();
      handlers!.forEach((h) => h(payload));
    };
    return { queryClient, fire };
  }

  it('writes a pushed status snapshot into the dashboard cache', () => {
    const { queryClient, fire } = mount();
    fire('status', { cpu: 42.5, mem: { current: 1, total: 2 }, xray: { state: 'running', version: '1.0.0' } });
    const cached = queryClient.getQueryData(keys.server.status());
    expect(cached).toBeInstanceOf(Status);
    expect((cached as Status).cpu.current).toBe(42.5);
    expect((cached as Status).xray.state).toBe('running');
  });

  it('ignores a status payload that is not a snapshot', () => {
    const { queryClient, fire } = mount();
    fire('status', 'running');
    expect(queryClient.getQueryData(keys.server.status())).toBeUndefined();
  });

  it('shows a pushed notification as a toast of its level', () => {
    const open = vi.spyOn(notification, 'open').mockImplementation(() => {});
    const { fire } = mount();
    fire('notification', { title: 'Xray restarted', message: 'done', level: 'success' });
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toMatchObject({ type: 'success', message: 'Xray restarted', description: 'done' });
  });

  it('refreshes the status snapshot on an xray_state change', () => {
    const { queryClient, fire } = mount();
    queryClient.setQueryData(keys.server.status(), new Status());
    fire('xray_state', { state: 'stop', errorMsg: '' });
    expect(queryClient.getQueryState(keys.server.status())?.isInvalidated).toBe(true);
  });
});
