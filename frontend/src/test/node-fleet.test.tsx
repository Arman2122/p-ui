import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { QueryClient } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import type { ComponentProps } from 'react';
import type { NodeRecord } from '@/schemas/node';
import NodeFleet from '@/pages/nodes/NodeFleet';
import NodeThroughput from '@/pages/nodes/NodeThroughput';
import type HistoryChart from '@/pages/index/HistoryChart';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('@/pages/index/HistoryChart', () => ({ default: (props: ComponentProps<typeof HistoryChart>) => <div data-testid="node-chart" data-times={JSON.stringify(props.times)} data-series={JSON.stringify(props.series.map(series => series.values))} /> }));
const text = (key: string) => String(i18next.t(key));
const freshNode = (): NodeRecord => ({ id: 7, guid: 'parent', name: 'Primary node', scheme: 'https', address: '192.0.2.7', port: 443, basePath: '/',
  enable: true, status: 'online', metricsAvailable: true, lastHeartbeat: Math.floor(Date.now() / 1000), cpuPct: 0, memPct: 36,
  netUp: 0, netDown: 1024, xrayState: 'running', xrayVersion: '26.7.28', panelVersion: '1.0.0',
  servedInbounds: [{ id: 2, tag: 'real-inbound-tag', protocol: 'vless', up: 10, down: 20 }],
});
const cacheClients: QueryClient[] = [];

function setup(nodes = [freshNode()], isMobile = false, history?: (url: string) => Msg<unknown>) {
  const actions = { onAdd: vi.fn(), onMtls: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onProbe: vi.fn(), onToggleEnable: vi.fn(), onUpdateNode: vi.fn(), onUpdateSelected: vi.fn(), onSelectionChange: vi.fn() };
  const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async url => history?.(url) ?? new Msg(true, '', []));
  const post = vi.spyOn(HttpUtil, 'post');
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  cacheClients.push(client);
  const view = renderWithProviders(<MemoryRouter><NodeFleet {...actions} nodes={nodes} isMobile={isMobile} selectedIds={[]} /></MemoryRouter>, { queryClient: client });
  return { ...view, ...actions, get, post, client };
}

afterEach(() => { cacheClients.splice(0).forEach(client => client.clear()); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

describe('node fleet confirmed fidelity gaps', () => {
  it('retains default cards and renders CPU, memory and truthful unreported disk slots', async () => {
    const view = setup();
    await waitFor(() => expect(view.get).toHaveBeenCalledTimes(2));
    expect((screen.getByRole('radio', { name: text('design.cardView') }) as HTMLInputElement).checked).toBe(true);
    expect(view.container.querySelectorAll('.node-resource')).toHaveLength(3);
    expect(view.container.querySelector('.node-resource')?.getAttribute('aria-label')).toBe(`${text('pages.nodes.cpu')}: 0%`);
    const disk = [...view.container.querySelectorAll('.node-resource')].find(element => element.textContent?.includes(text('nodesFidelity.disk')))!;
    expect(disk.textContent).toContain('—');
    expect(disk.textContent).toContain(text('nodesFidelity.notReported'));
    expect(disk.textContent).not.toContain('0.0');
    expect(screen.getByText('real-inbound-tag · vless')).toBeTruthy();
    expect(screen.queryByTestId('node-chart')).toBeNull();
    expect(view.post).not.toHaveBeenCalled();
  });

  it('honors explicit Table mode on mobile and retains the search when switching back', async () => {
    setup([freshNode(), { ...freshNode(), id: 8, name: 'Other node' }], true);
    fireEvent.change(screen.getByRole('searchbox', { name: text('design.searchNodes') }), { target: { value: 'Primary' } });
    fireEvent.click(screen.getByRole('radio', { name: text('design.tableView') }));
    expect(screen.getByRole('table')).toBeTruthy();
    expect(screen.queryByText('Other node')).toBeNull();
    fireEvent.click(screen.getByRole('radio', { name: text('design.cardView') }));
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByRole('button', { name: 'Primary node' })).toBeTruthy();
    expect((screen.getByRole('searchbox', { name: text('design.searchNodes') }) as HTMLInputElement).value).toBe('Primary');
  });

  it('keeps direct-node actions wired and transitive nodes read-only without querying node zero', async () => {
    const parent = freshNode();
    const view = setup([parent,
      { id: 0, guid: 'child-one', parentGuid: 'parent', transitive: true, name: 'Child one', status: 'online' },
      { id: 0, guid: 'child-two', parentGuid: 'parent', transitive: true, name: 'Child two', status: 'online' },
    ]);
    await waitFor(() => expect(view.get).toHaveBeenCalledTimes(2));
    expect(view.get.mock.calls.every(([url]) => String(url).includes('/history/7/'))).toBe(true);
    const parentCard = screen.getByRole('button', { name: 'Primary node' }).closest('section')!;
    fireEvent.click(within(parentCard).getByRole('button', { name: text('edit') }));
    fireEvent.click(within(parentCard).getByRole('button', { name: text('check') }));
    fireEvent.click(within(parentCard).getByRole('button', { name: text('delete') }));
    fireEvent.click(within(parentCard).getByRole('switch'));
    expect(view.onEdit).toHaveBeenCalledWith(parent);
    expect(view.onProbe).toHaveBeenCalledWith(parent);
    expect(view.onDelete).toHaveBeenCalledWith(parent);
    expect(view.onToggleEnable).toHaveBeenCalledWith(parent, false);
    for (const name of ['Child one', 'Child two']) {
      const card = screen.getByRole('button', { name }).closest('section')!;
      expect(within(card).queryByRole('switch')).toBeNull();
      expect(within(card).queryByRole('button', { name: text('edit') })).toBeNull();
      expect(within(card).queryByRole('checkbox')).toBeNull();
    }
    fireEvent.click(screen.getByRole('button', { name: text('pages.nodes.mtls.title') }));
    expect(view.onMtls).toHaveBeenCalledTimes(1);
  });

  it('does not reuse stale current readings or invent history lines', async () => {
    const view = setup([{ ...freshNode(), lastHeartbeat: Math.floor(Date.now() / 1000) - 120, cpuPct: 91, memPct: 93 }]);
    await waitFor(() => expect(view.get).toHaveBeenCalledTimes(2));
    expect([...view.container.querySelectorAll('.node-resource')].every(element => element.getAttribute('aria-label')?.endsWith(text('nodesFidelity.notReported')))).toBe(true);
    expect(view.container.querySelector('.node-throughput-rates')?.textContent).toContain('↑ —');
    expect(screen.queryByTestId('node-chart')).toBeNull();
    expect(await screen.findByText(text('telemetry.noReadings'))).toBeTruthy();
  });

  it('loads both real directions into a bounded visible chart and retains missing-minute gaps', async () => {
    const end = Math.floor(Date.now() / 60_000) * 60;
    const view = setup([freshNode()], false, url => new Msg(true, '', url.includes('/netUp/')
      ? [{ t: end - 3600, v: 999 }, { t: end - 120, v: 0 }, { t: end, v: 2048 }]
      : [{ t: end - 120, v: 1024 }, { t: end, v: 4096 }]));
    const chart = await screen.findByTestId('node-chart');
    const times = JSON.parse(chart.getAttribute('data-times')!) as number[];
    const series = JSON.parse(chart.getAttribute('data-series')!) as (number | null)[][];
    expect(times).toHaveLength(30);
    expect(times.at(-1)).toBe(end);
    expect(series[0].slice(-3)).toEqual([0, null, 2048]);
    expect(series[1].slice(-3)).toEqual([1024, null, 4096]);
    expect(series[0]).not.toContain(999);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(view.post).not.toHaveBeenCalled();
  });

  it('reports history failures and retries instead of substituting zero readings', async () => {
    const view = setup([freshNode()], false, () => new Msg(false, 'History backend failed'));
    await screen.findByText(text('telemetry.historyUnavailable'));
    expect(screen.queryByTestId('node-chart')).toBeNull();
    view.get.mockResolvedValue(new Msg(true, '', [{ t: Math.floor(Date.now() / 60_000) * 60, v: 0 }]));
    fireEvent.click(screen.getByRole('button', { name: text('refresh') }));
    expect(await screen.findByTestId('node-chart')).toBeTruthy();
  });

  it('polls only near visible cards and aborts history requests when unmounted', async () => {
    let notify: IntersectionObserverCallback = () => {};
    const observer = { observe: vi.fn(), disconnect: vi.fn(), unobserve: vi.fn(), takeRecords: () => [], root: null, rootMargin: '120px', scrollMargin: '0px', thresholds: [] };
    vi.stubGlobal('IntersectionObserver', class { constructor(callback: IntersectionObserverCallback) { notify = callback; return observer; } });
    const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async (_url, _params, options) => new Promise((_resolve, reject) => { options?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))); }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    cacheClients.push(client);
    const view = renderWithProviders(<NodeThroughput node={freshNode()} />, { queryClient: client });
    expect(get).not.toHaveBeenCalled();
    act(() => notify([{ isIntersecting: true } as IntersectionObserverEntry], observer));
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    const signal = get.mock.calls[0][2]?.signal;
    expect(signal?.aborted).toBe(false);
    view.unmount();
    expect(signal?.aborted).toBe(true);
    expect(observer.disconnect).toHaveBeenCalled();
  });

  it('pauses periodic history reads when a card leaves the visible region', async () => {
    vi.useFakeTimers();
    let notify: IntersectionObserverCallback = () => {};
    const observer = { observe: vi.fn(), disconnect: vi.fn(), unobserve: vi.fn(), takeRecords: () => [], root: null, rootMargin: '120px', scrollMargin: '0px', thresholds: [] };
    vi.stubGlobal('IntersectionObserver', class { constructor(callback: IntersectionObserverCallback) { notify = callback; return observer; } });
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    cacheClients.push(client);
    renderWithProviders(<NodeThroughput node={freshNode()} />, { queryClient: client });
    await act(async () => { notify([{ isIntersecting: true } as IntersectionObserverEntry], observer); await vi.advanceTimersByTimeAsync(0); });
    expect(get).toHaveBeenCalledTimes(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(15_001); });
    expect(get).toHaveBeenCalledTimes(4);
    await act(async () => { notify([{ isIntersecting: false } as IntersectionObserverEntry], observer); await vi.advanceTimersByTimeAsync(0); });
    await act(async () => { await vi.advanceTimersByTimeAsync(45_000); });
    expect(get).toHaveBeenCalledTimes(4);
  });
});
