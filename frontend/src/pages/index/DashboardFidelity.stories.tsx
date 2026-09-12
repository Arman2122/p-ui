import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, within } from 'storybook/test';
import { Button } from 'antd';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { clearNotifications, pushNotification } from '@/api/notificationFeed';
import DashboardCores, { CoreSummaryStrip } from './DashboardCores';
import VitalsBand, { type VitalKey } from './VitalsBand';
import { ActivityPanel, PanelHostPanel } from './DashboardPanels';
import { HistoryWindowControl, SessionsPanel, ThroughputPanel } from './DashboardTelemetry';
import { dashboardCores, dashboardHistory, dashboardStatus } from './Dashboard.fixtures';
import './IndexPage.css';

function DashboardSurface() {
  const { t } = useTranslation();
  const [open, setOpen] = useState<VitalKey | null>(null);
  const [showIp, setShowIp] = useState(false);
  const [window, setWindow] = useState(60);
  const [refreshes, setRefreshes] = useState(0);
  return <main className="p-root index-page dashboard-story-frame" style={{ width: '100%', maxWidth: 2007, margin: '0 auto', padding: 20, containerType: 'inline-size', containerName: 'main', background: 'var(--p-bg)' }}>
    <div className="ov-page">
      <PageHeader title={t('menu.dashboard')} sub={t('dashboardRepair.subtitle')} actions={<><HistoryWindowControl value={window} onChange={setWindow} /><Button onClick={() => setRefreshes((value) => value + 1)}>{t('refresh')}</Button></>} />
      <VitalsBand status={dashboardStatus} history={dashboardHistory} open={open} onOpen={setOpen} lead={<CoreSummaryStrip cores={dashboardCores} onOpen={() => undefined} />} />
      <PanelCard className="ov-cores-panel" kicker={t('menu.cores')} icon="xray" pad={12} extra={<Button type="text">{t('menu.manageCores')}</Button>}><DashboardCores cores={dashboardCores} onOpen={() => undefined} /></PanelCard>
      <div className="ov-telemetry-grid"><ThroughputPanel status={dashboardStatus} history={dashboardHistory} /><SessionsPanel status={dashboardStatus} history={dashboardHistory} /></div>
      <div className="ov-bottom-grid"><ActivityPanel relative={() => '2m ago'} /><PanelHostPanel status={dashboardStatus} nodeCount={3} webPort={16109} basePath="/preview/" sessionMinutes={360} showIp={showIp} onToggleIp={() => setShowIp((value) => !value)} /></div>
      <output hidden>{refreshes}</output>
    </div>
  </main>;
}

const meta = {
  title: 'Pages/Dashboard', component: DashboardSurface,
  parameters: { layout: 'fullscreen', viewport: { defaultViewport: 'reference', viewports: { reference: { name: 'Reference desktop', styles: { width: '2235px', height: '1240px' }, type: 'desktop' } } } },
  beforeEach: () => {
    clearNotifications();
    for (const [title, message] of [['Client connected', 'One device online'], ['Quota notice', 'Traffic reached the warning threshold'], ['Routing rule edited', 'Saved destination selection'], ['Node restarting', 'Core configuration updated'], ['Client created', 'Assigned to the selected plan'], ['Client disabled', 'Quota depleted']]) {
      pushNotification({ level: 'info', title, message });
    }
    return () => clearNotifications();
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(canvasElement.querySelectorAll('.p-coretile')).toHaveLength(8);
    expect(canvasElement.querySelectorAll('.p-corechip')).toHaveLength(8);
    const container = canvasElement.querySelector<HTMLElement>('.dashboard-story-frame')!;
    expect(container.scrollWidth).toBeLessThanOrEqual(container.clientWidth + 1);
    if (window.innerWidth > 1800) {
      expect(canvasElement.querySelector('.p-vitals')!.getBoundingClientRect().height).toBeLessThanOrEqual(110);
      const throughput = canvasElement.querySelector('.ov-throughput-panel')!.getBoundingClientRect();
      const sessions = canvasElement.querySelector('.ov-sessions-panel')!.getBoundingClientRect();
      expect(throughput.width / sessions.width).toBeGreaterThan(2);
      expect(throughput.width / sessions.width).toBeLessThan(2.1);
      expect(Math.abs(throughput.y - sessions.y)).toBeLessThan(1);
    }
    const gauges = Array.from(canvasElement.querySelectorAll<HTMLButtonElement>('.p-vital'));
    for (const gauge of gauges) {
      await userEvent.click(gauge);
      expect(gauge.getAttribute('aria-expanded')).toBe('true');
      expect(canvasElement.querySelectorAll('.p-vitals-x')).toHaveLength(1);
      await userEvent.click(gauge);
      expect(canvasElement.querySelectorAll('.p-vitals-x')).toHaveLength(0);
    }
    const ipButton = canvas.getByRole('button', { name: /visibility|نمایش/ });
    await userEvent.click(ipButton);
    expect(canvasElement.querySelectorAll('.ov-host-addresses .ip-hidden')).toHaveLength(0);
    await userEvent.keyboard('{Enter}');
    expect(canvasElement.querySelectorAll('.ov-host-addresses .ip-hidden')).toHaveLength(2);
  },
} satisfies Meta<typeof DashboardSurface>;
export default meta;
type Story = StoryObj<typeof meta>;
export const DesktopDark: Story = { globals: { theme: 'dark' } };
export const DesktopLight: Story = {};
export const MobileDark: Story = { globals: { theme: 'dark' }, parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
