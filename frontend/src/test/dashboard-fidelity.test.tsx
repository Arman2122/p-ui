import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { SessionsPanel, ThroughputPanel } from '@/pages/index/DashboardTelemetry';
import { dashboardHistory, dashboardStatus } from '@/pages/index/Dashboard.fixtures';
import { Status } from '@/models/status';
import i18next from 'i18next';

vi.mock('@/pages/index/HistoryChart', () => ({ default: ({ series }: { series: { values: number[] }[] }) => <output aria-label="plotted samples">{JSON.stringify(series.map((item) => item.values))}</output> }));

describe('dashboard reference telemetry', () => {
  it('keeps the handshake slot unreported and derives the window peak only from complete socket observations', () => {
    const history = { ...dashboardHistory, times: [1, 2, 3], series: { ...dashboardHistory.series, tcpCount: [0, 20, Number.NaN], udpCount: [0, 3, 100] } };
    render(<SessionsPanel status={dashboardStatus} history={history} />);
    expect(screen.getByLabelText('plotted samples').textContent).toBe('[[0,23,null]]');
    const footer = document.querySelector('.ov-session-footer')! as HTMLElement;
    expect(within(footer).getByText('23')).toBeTruthy();
    expect(within(footer).getByText(i18next.t('dashboardRepair.handshakes'))).toBeTruthy();
    expect(within(footer).getByText('—')).toBeTruthy();
    expect(within(footer).getByText(i18next.t('dashboardRepair.unreported'))).toBeTruthy();
  });

  it('does not replace unavailable socket observations with a zero total or peak', () => {
    const status = new Status({ tcpCount: 4, udpCount: 0, unavailable: ['udpCount'] });
    const history = { ...dashboardHistory, times: [1], series: { ...dashboardHistory.series, tcpCount: [4], udpCount: [Number.NaN] } };
    render(<SessionsPanel status={status} history={history} />);
    expect(document.querySelector('.ov-session-current .p-stat')?.textContent).toBe('—');
    expect(document.querySelector('.ov-session-footer .p-val')?.textContent).toBe('—');
    expect(screen.getByLabelText('plotted samples').textContent).toBe('[[null]]');
  });

  it('plots a real zero throughput sample while withholding unavailable current counters', () => {
    const status = new Status({ netIO: { up: 0, down: 0 }, unavailable: ['netTraffic', 'netUp', 'netDown'] });
    const history = { ...dashboardHistory, times: [1], series: { ...dashboardHistory.series, netUp: [0], netDown: [0] } };
    render(<ThroughputPanel status={status} history={history} />);
    expect(screen.getByLabelText('plotted samples').textContent).toBe('[[0],[0]]');
    const footer = document.querySelector('.ov-throughput-footer')! as HTMLElement;
    expect(within(footer).getAllByText('—')).toHaveLength(2);
    expect(screen.queryByText(i18next.t('telemetry.noReadings'))).toBeNull();
  });
});
