import type { ReactNode } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useXraySetting } from '@/hooks/useXraySetting';
import { exitKeyFor } from '@/schemas/api/egress';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient } from './test-utils';

const proxy = (tag: string) => ({ tag, protocol: 'socks', settings: { servers: [{ address: `${tag}.example`, port: 443 }] } });
const response = (delay: number, count = 1) => new Msg(true, '', Array.from({ length: count }, () => ({ success: true, delay, mode: 'http' })));
function deferred() {
  let resolve!: (value: Msg<unknown>) => void;
  const promise = new Promise<Msg<unknown>>((done) => { resolve = done; });
  return { resolve, promise };
}
async function setup(outbounds = [proxy('a'), proxy('b')], subscriptions = [proxy('sub')]) {
  let payload = { xraySetting: { outbounds }, outboundTestUrl: 'https://test.example', subscriptionOutbounds: subscriptions };
  const pending: ReturnType<typeof deferred>[] = [];
  const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => {
    if (url === '/panel/api/xray/') return new Msg(true, '', JSON.stringify(payload));
    const request = deferred();
    pending.push(request);
    return request.promise;
  });
  vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
  const client = makeTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const hook = renderHook(useXraySetting, { wrapper });
  await waitFor(() => expect(hook.result.current.templateSettings?.outbounds).toHaveLength(outbounds.length));
  return { ...hook, pending, post, setSubscriptions: (next: typeof subscriptions) => { payload = { ...payload, subscriptionOutbounds: next }; } };
}
afterEach(() => vi.restoreAllMocks());

describe('outbound probe request identity', () => {
  it('discards a response that arrives after the draft outbound was edited', async () => {
    const { result, pending } = await setup();
    let request!: Promise<unknown>;
    act(() => { request = result.current.testOutbound(0, proxy('a')); });
    act(() => result.current.setTemplateSettings((prev) => ({ ...prev, outbounds: [proxy('changed'), proxy('b')] })));
    await act(async () => { pending[0].resolve(response(12)); await request; });
    expect(result.current.outboundTestStates[0]).toBeUndefined();
  });

  it.each(['reorder', 'delete', 'raw', 'url'] as const)('clears cached template and subscription results on %s while preserving independent host probes', async (change) => {
    const { result, pending } = await setup();
    let requests!: Promise<unknown>[];
    act(() => { requests = [result.current.testOutbound(0, proxy('a')), result.current.testSubscriptionOutbound('sub', proxy('sub')), result.current.testExit(exitKeyFor(9))]; });
    await act(async () => { pending.forEach((request) => request.resolve(response(30))); await Promise.all(requests); });
    expect(result.current.outboundTestStates[0].result?.delay).toBe(30);
    act(() => {
      if (change === 'reorder') result.current.setTemplateSettings((prev) => ({ ...prev, outbounds: [...prev!.outbounds!].reverse() }));
      if (change === 'delete') result.current.setTemplateSettings((prev) => ({ ...prev, outbounds: prev!.outbounds!.slice(1) }));
      if (change === 'raw') result.current.setXraySetting(JSON.stringify({ outbounds: [proxy('new')] }));
      if (change === 'url') result.current.setOutboundTestUrl('https://different.example');
    });
    expect(result.current.outboundTestStates[0]).toBeUndefined();
    expect(result.current.subscriptionTestStates).toEqual({});
    expect(result.current.outboundTestStates[exitKeyFor(9)].result?.delay).toBe(30);
  });

  it('invalidates cached and outstanding subscription probes after a same-tag server refresh', async () => {
    const { result, pending, setSubscriptions } = await setup();
    let request!: Promise<unknown>;
    act(() => { request = result.current.testSubscriptionOutbound('sub', proxy('sub')); });
    setSubscriptions([{ ...proxy('sub'), settings: { servers: [{ address: 'changed.example', port: 8443 }] } }]);
    await act(async () => result.current.fetchAll());
    await waitFor(() => expect(result.current.subscriptionTestStates).toEqual({}));
    await act(async () => { pending[0].resolve(response(12)); await request; });
    expect(result.current.subscriptionTestStates).toEqual({});
  });

  it('does not clear valid cached results when an identical config is refreshed', async () => {
    const { result, pending } = await setup();
    let request!: Promise<unknown>;
    act(() => { request = result.current.testOutbound(0, proxy('a')); });
    await act(async () => { pending[0].resolve(response(12)); await request; });
    await act(async () => result.current.fetchAll());
    expect(result.current.outboundTestStates[0].result?.delay).toBe(12);
  });

  it('stops remaining HTTP chunks and drops in-flight batch results after a reorder', async () => {
    const { result, pending, post } = await setup(Array.from({ length: 18 }, (_, i) => proxy(`p${i}`)));
    let request!: Promise<unknown>;
    act(() => { request = result.current.testAllOutbounds('http'); });
    expect(pending).toHaveLength(1);
    act(() => result.current.setTemplateSettings((prev) => ({ ...prev, outbounds: [...prev!.outbounds!].reverse() })));
    await act(async () => { pending[0].resolve(response(12, 16)); await Promise.resolve(); });
    expect(post.mock.calls.filter(([url]) => url === '/panel/api/xray/testOutbounds')).toHaveLength(1);
    await act(async () => request);
    expect(result.current.outboundTestStates).toEqual({});
    expect(result.current.subscriptionTestStates).toEqual({});
    expect(result.current.testingAll).toBe(false);
  });

  it.each(['individual-last', 'batch-last'] as const)('allows only the latest per-key request to publish when %s', async (order) => {
    const { result, pending } = await setup([proxy('a')], []);
    let individual!: Promise<unknown>, batch!: Promise<unknown>;
    act(() => {
      if (order === 'individual-last') { batch = result.current.testAllOutbounds('http'); individual = result.current.testOutbound(0, proxy('a'), 'http'); }
      else { individual = result.current.testOutbound(0, proxy('a'), 'http'); batch = result.current.testAllOutbounds('http'); }
    });
    await act(async () => { pending[1].resolve(response(22)); await (order === 'individual-last' ? individual : batch); });
    expect(result.current.outboundTestStates[0].result?.delay).toBe(22);
    await act(async () => { pending[0].resolve(response(99)); await (order === 'individual-last' ? batch : individual); });
    expect(result.current.outboundTestStates[0].result?.delay).toBe(22);
  });

  it('keeps the latest subscription request when overlapping probes finish out of order', async () => {
    const { result, pending } = await setup();
    let older!: Promise<unknown>, newer!: Promise<unknown>;
    act(() => { older = result.current.testSubscriptionOutbound('sub', proxy('sub')); newer = result.current.testSubscriptionOutbound('sub', proxy('sub')); });
    await act(async () => { pending[1].resolve(response(22)); await newer; });
    await act(async () => { pending[0].resolve(response(99)); await older; });
    expect(result.current.subscriptionTestStates.sub.result?.delay).toBe(22);
  });

  it('never lets a queued later HTTP chunk supersede an individual probe started after Test All', async () => {
    const { result, pending, post } = await setup(Array.from({ length: 17 }, (_, i) => proxy(`p${i}`)), []);
    let batch!: Promise<unknown>, individual!: Promise<unknown>;
    act(() => { batch = result.current.testAllOutbounds('http'); individual = result.current.testOutbound(16, proxy('p16'), 'http'); });
    await act(async () => { pending[1].resolve(response(22)); await individual; });
    await act(async () => { pending[0].resolve(response(99, 16)); await Promise.resolve(); });
    expect(post.mock.calls.filter(([url]) => url === '/panel/api/xray/testOutbounds')).toHaveLength(2);
    await act(async () => batch);
    expect(result.current.outboundTestStates[16].result?.delay).toBe(22);
    expect(result.current.outboundTestStates[0].result?.delay).toBe(99);
  });

  it('does not let an invalidated batch completion release a newer Test All run', async () => {
    const { result, pending } = await setup([proxy('a')], []);
    let oldRun!: Promise<unknown>, newRun!: Promise<unknown>;
    act(() => { oldRun = result.current.testAllOutbounds('http'); });
    act(() => result.current.setOutboundTestUrl('https://changed.example'));
    act(() => { newRun = result.current.testAllOutbounds('http'); });
    await act(async () => { pending[0].resolve(response(99)); await oldRun; });
    expect(result.current.testingAll).toBe(true);
    expect(result.current.outboundTestStates[0].testing).toBe(true);
    await act(async () => { pending[1].resolve(response(22)); await newRun; });
    expect(result.current.testingAll).toBe(false);
    expect(result.current.outboundTestStates[0].result?.delay).toBe(22);
  });

  it('clears pending results immediately when raw JSON becomes incomplete', async () => {
    const { result, pending } = await setup();
    let request!: Promise<unknown>;
    act(() => { request = result.current.testOutbound(0, proxy('a')); });
    act(() => result.current.setXraySetting('{"outbounds":['));
    await act(async () => { pending[0].resolve(response(99)); await request; });
    expect(result.current.outboundTestStates).toEqual({});
  });
});
