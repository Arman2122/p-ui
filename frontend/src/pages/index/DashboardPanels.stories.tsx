import type { Meta, StoryObj } from '@storybook/react-vite';

import { Status } from '@/models/status';
import { clearNotifications, pushNotification } from '@/api/notificationFeed';
import { ActivityPanel, PanelHostPanel, SessionsPanel, ThroughputPanel } from './DashboardPanels';
import type { OverviewHistory } from './useOverviewHistory';
import '@/styles/panel.css';
import './IndexPage.css';

const GiB = 1024 ** 3;

function wave(n: number, base: number, amp: number, seed: number): number[] {
  return Array.from({ length: n }, (_, i) => Math.max(0, +(base + Math.sin(i / 5 + seed) * amp * 0.7 + ((i * seed) % 9) * amp * 0.04).toFixed(2)));
}

const status = new Status({
  cpu: 2.2,
  cpuCores: 2,
  logicalPro: 2,
  cpuSpeedMhz: 2100,
  mem: { current: 0.82 * GiB, total: 3.73 * GiB },
  swap: { current: 0, total: 0 },
  disk: { current: 17.15 * GiB, total: 37.21 * GiB },
  netIO: { up: 18_860, down: 7_220 },
  netTraffic: { sent: 2.91 * GiB, recv: 9.5 * GiB },
  tcpCount: 110,
  udpCount: 15,
  uptime: 25 * 86400,
  appStats: { threads: 45, mem: 67.96 * 1024 * 1024, uptime: 160_000 },
  publicIP: { ipv4: '91.107.151.123', ipv6: '2a01:4f8:c17:2e::1' },
  xray: { state: 'running', version: '26.7.28', errorMsg: '' },
});

const history: OverviewHistory = {
  times: Array.from({ length: 60 }, (_, i) => Date.UTC(2026, 8, 13, 12) / 1000 + i * 60),
  labels: Array.from({ length: 60 }, (_, i) => `12:${String(i).padStart(2, '0')}:00`),
  series: {
    cpu: wave(60, 2.4, 2.2, 7),
    mem: wave(60, 21.4, 1.1, 3),
    swap: wave(60, 0, 0, 5),
    diskUsage: wave(60, 46, 0.4, 11),
    netUp: wave(60, 210_000, 120_000, 13),
    netDown: wave(60, 96_000, 64_000, 17),
    tcpCount: wave(60, 110, 12, 19),
    udpCount: wave(60, 15, 4, 23),
  },
};

/* The feed is a module-level store, so a story seeds it the same way the socket
   would. Clearing first keeps a re-render from stacking duplicates. */
function seedFeed() {
  clearNotifications();
  pushNotification({ level: 'error', title: 'Core stopped', titleKey: 'events.coreCrash.title', messageKey: 'events.coreCrash.body', params: { core: 'singbox', error: 'exit status 1' } });
  pushNotification({ level: 'warning', title: 'Device limit passed', titleKey: 'events.deviceLimit.title', messageKey: 'events.deviceLimit.body', params: { email: 'roham', devices: '4', limit: '2', ip: '5.5.5.5' } });
  pushNotification({ level: 'success', title: 'Node back online', titleKey: 'events.nodeUp.title', messageKey: 'events.nodeUp.body', params: { node: 'fra-02', latency: '28' } });
  pushNotification({ level: 'warning', title: 'Failed sign-in', titleKey: 'events.loginFail.title', messageKey: 'events.loginFail.body', params: { user: 'root', ip: '9.9.9.9' } });
}

const meta = {
  title: 'Dashboard/Panels',
  component: ThroughputPanel,
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component:
          'The lower half of the dashboard: throughput, live sessions, activity and panel/host. Throughput and sessions read the 2s status poll plus the rolling history window; activity reads the notification feed the event-bus subscriber fills; panel/host mixes the status poll with the nodes query and panel settings.',
      },
    },
  },
} satisfies Meta<typeof ThroughputPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

const frame = { padding: 20, background: 'var(--p-bg)', display: 'flex', flexDirection: 'column' as const, gap: 16 };

export const Everything: Story = {
  args: { status, history },
  render: () => {
    seedFeed();
    return (
      <div className="p-root index-page ov-page" style={{ ...frame, containerType: 'inline-size', containerName: 'main' }}>
        <div className="ov-telemetry-grid">
          <ThroughputPanel status={status} history={history} />
          <SessionsPanel status={status} history={history} />
        </div>
        <div className="ov-bottom-grid">
          <ActivityPanel relative={(at) => `${Math.max(1, Math.round((Date.now() - at) / 1000))}s ago`} />
          <PanelHostPanel
            status={status}
            nodeCount={3}
            webPort={16109}
            basePath="/em2qCdwst6wzLg9orw/"
            sessionMinutes={360}
            showIp={false}
            onToggleIp={() => undefined}
          />
        </div>
      </div>
    );
  },
};

export const ActivityEmpty: Story = {
  args: { status, history },
  render: () => {
    clearNotifications();
    return (
      <div className="p-root" style={frame}>
        <ActivityPanel relative={() => 'just now'} />
      </div>
    );
  },
};
