import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { HttpUtil } from '@/utils';
import { alignHistory, mean, peak } from '@/pages/index/useOverviewHistory';
import { ClientPlanCell, ClientTrendCell } from '@/pages/clients/ClientMetadataCells';
import GroupQuotaModal from '@/pages/groups/GroupQuotaModal';
import NodeHistoryPanel from '@/pages/nodes/NodeHistoryPanel';
import { nodeHasReadings } from '@/pages/nodes/telemetry';

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/pages/index/HistoryChart', () => ({ default: () => <div data-testid="history-chart" /> }));

function wrap(child: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}>{child}</QueryClientProvider>);
}

describe('design telemetry', () => {
  it('aligns timestamps and retains real zeroes without manufacturing missing observations', () => {
    const result = alignHistory({ bucket: 60, end: 6000, series: {
      netUp: [{ t: 5940, v: 0 }, { t: 6000, v: 200 }], netDown: [{ t: 6000, v: 400 }],
    } });
    expect(result.times?.slice(-2)).toEqual([5940, 6000]);
    expect(result.series.netUp.slice(-2)).toEqual([0, 200]);
    expect(result.series.netDown.at(-2)).toBeNaN();
    expect(mean(result.series.netUp)).toBe(100);
    expect(peak(result.series.netDown)).toBe(400);
    expect(mean(result.series.cpu)).toBeNaN();
  });

  it('distinguishes custom and unresolved plans, and absent history', () => {
    render(<><ClientPlanCell client={{ email: 'custom' }} /><ClientTrendCell client={{ email: 'custom' }} />
      <ClientPlanCell client={{ email: 'assigned', plan: { name: 'Monthly', policyId: 1, unresolved: false, pending: true, overridden: true } }} />
      <ClientPlanCell client={{ email: 'missing', plan: { name: '', policyId: null, unresolved: true, pending: false, overridden: false } }} /></>);
    expect(screen.getByText('telemetry.customPlan')).toBeTruthy();
    expect(screen.getByText('telemetry.noReadings')).toBeTruthy();
    expect(screen.getByText('Monthly')).toBeTruthy();
    expect(screen.getByText('telemetry.planPending')).toBeTruthy();
    expect(screen.getByText('telemetry.unresolvedPlan')).toBeTruthy();
    expect(screen.queryByTestId('history-chart')).toBeNull();
  });

  it('persists the pooled quota in bytes and keeps the modal open after a rejected save', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: false, msg: 'Quota rejected', obj: null, warning: '' });
    const close = vi.fn();
    wrap(<GroupQuotaModal group={{ name: 'family', quotaBytes: 1073741824, clientCount: 2, trafficUsed: 10, up: 5, down: 5 }} onClose={close} onSaved={vi.fn()} />);
    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/clients/groups/quota',
      { name: 'family', quotaBytes: 2147483648 }, { headers: { 'Content-Type': 'application/json' } }));
    expect(await screen.findByText('Quota rejected')).toBeTruthy();
    expect(close).not.toHaveBeenCalled();
  });

  it('requests the selected node history window instead of relabeling existing data', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: [], msg: '', warning: '' });
    wrap(<NodeHistoryPanel node={{ id: 42 }} />);
    await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/nodes/history/42/netDown/60', undefined, { silent: true }));
    fireEvent.click(screen.getByText('telemetry.day'));
    await waitFor(() => expect(get).toHaveBeenCalledWith('/panel/api/nodes/history/42/netDown/1440', undefined, { silent: true }));
    expect(screen.queryByTestId('history-chart')).toBeNull();
  });

  it('includes explicitly edited shared inbounds in the group save', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, msg: '', obj: null, warning: '' });
    wrap(<GroupQuotaModal group={{ name: 'family', clientCount: 0, trafficUsed: 0, up: 0, down: 0 }}
      inbounds={[{ id: 2, remark: 'Second', protocol: 'vless' }]} onClose={vi.fn()} onSaved={vi.fn()} />);
    fireEvent.mouseDown(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByText('Second · vless'));
    fireEvent.click(screen.getByRole('button', { name: 'save' }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/clients/groups/quota',
      { name: 'family', quotaBytes: 0, inboundIds: [2] }, { headers: { 'Content-Type': 'application/json' } }));
  });

  it('never presents stale or transitive node readings as live load', () => {
    const now = 1789227000000;
    const node = { id: 1, enable: true, status: 'online', metricsAvailable: true, lastHeartbeat: now / 1000 - 10 };
    expect(nodeHasReadings(node, now)).toBe(true);
    expect(nodeHasReadings({ ...node, status: 'offline' }, now)).toBe(false);
    expect(nodeHasReadings({ ...node, transitive: true }, now)).toBe(false);
    expect(nodeHasReadings({ ...node, lastHeartbeat: now / 1000 - 100 }, now)).toBe(false);
  });
});
