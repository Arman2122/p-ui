import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, expect, it, vi } from 'vitest';
import { useXraySetting } from '@/hooks/useXraySetting';
import { makeTestQueryClient } from './test-utils';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => vi.restoreAllMocks());

it('sends the explicit normalized snapshot once and retains edits made while save is pending', async () => {
  let finish!: (msg: Msg<unknown>) => void;
  const pending = new Promise<Msg<unknown>>((resolve) => { finish = resolve; });
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => url === '/panel/api/xray/'
    ? new Msg(true, '', JSON.stringify({ xraySetting: { outbounds: [] }, outboundTestUrl: 'https://test.example' })) : pending);
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  const client = makeTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const { result } = renderHook(useXraySetting, { wrapper });
  await waitFor(() => expect(result.current.templateSettings).not.toBeNull());
  const snapshot = JSON.stringify({ outbounds: [{ tag: 'direct', protocol: 'freedom' }] });
  const later = JSON.stringify({ outbounds: [{ tag: 'later', protocol: 'freedom' }] });
  let save!: Promise<void>;
  act(() => { save = result.current.saveAll(snapshot); });
  act(() => result.current.setXraySetting(later));
  await waitFor(() => expect(post.mock.calls.filter(([url]) => url === '/panel/api/xray/update')).toHaveLength(1));
  expect(post).toHaveBeenCalledWith('/panel/api/xray/update', { xraySetting: snapshot, outboundTestUrl: 'https://test.example' });
  await act(async () => { finish(new Msg(true, '')); await save; });
  expect(result.current.xraySetting).toBe(later);
  expect(result.current.saveDisabled).toBe(false);
});
