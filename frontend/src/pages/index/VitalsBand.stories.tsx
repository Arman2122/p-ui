import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { Button } from 'antd';
import type { CoreStatusView } from '@/generated/zod';

import { PanelCard } from '@/components/ui/panel';
import { Status } from '@/models/status';
import VitalsBand, { type VitalKey } from './VitalsBand';
import DashboardCores, { CoreSummaryStrip } from './DashboardCores';
import type { OverviewHistory } from './useOverviewHistory';
import '@/styles/panel.css';

const GiB = 1024 ** 3;

/* A deterministic wobble so the sparklines look like telemetry without a
   random source, which would make the snapshot differ on every render. */
function wave(n: number, base: number, amp: number, seed: number): number[] {
  return Array.from({ length: n }, (_, i) => Math.max(0, +(base + Math.sin(i / 4 + seed) * amp * 0.6 + ((i * seed) % 7) * amp * 0.05).toFixed(2)));
}

const status = new Status({
  cpu: 2.2,
  cpuCores: 2,
  logicalPro: 2,
  cpuSpeedMhz: 2100,
  loads: [0.18, 0.24, 0.21],
  mem: { current: 0.82 * GiB, total: 3.73 * GiB },
  swap: { current: 0, total: 0 },
  disk: { current: 17.15 * GiB, total: 37.21 * GiB },
  netIO: { up: 18860, down: 7220 },
  netTraffic: { sent: 2.91 * GiB, recv: 9.5 * GiB },
  tcpCount: 110,
  udpCount: 15,
  uptime: 25 * 86400,
  appStats: { threads: 45, mem: 67.96 * 1024 * 1024, uptime: 160000 },
  publicIP: { ipv4: '91.107.151.123', ipv6: '2a01:4f8:c17:2e::1' },
  xray: { state: 'running', version: '26.7.28', errorMsg: '' },
});

const history: OverviewHistory = {
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

function core(id: string, titleKey: string, state: string, ready: boolean, installed?: string, sinceHoursAgo?: number): CoreStatusView {
  return {
    id,
    titleKey,
    state,
    ready,
    available: true,
    logs: true,
    instances: [],
    version: { manageable: id === 'xray', reported: !!installed, installed },
    since: sinceHoursAgo ? Date.now() - sinceHoursAgo * 3600_000 : undefined,
  };
}

const cores: CoreStatusView[] = [
  core('xray', 'cores.xray.title', 'running', true, '26.7.28', 46),
  core('singbox', 'cores.singbox.title', 'running', true, '1.10.3', 6),
  core('wgkernel', 'cores.wgkernel.title', 'running', true, '1.0.20210914'),
  core('awgkernel', 'cores.awgkernel.title', 'running', true, '1.0.20241018'),
  core('ssh', 'cores.ssh.title', 'stopped', false),
  core('mtproto', 'cores.mtproto.title', 'idle', false, '1.9.0'),
];

const meta = {
  title: 'Dashboard/Vitals band',
  component: VitalsBand,
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component:
          'The dashboard vitals band from the Penhoon Panel design: a core-summary lead cell plus four gauges (CPU, Memory, Swap, Disk). Clicking a gauge expands a sparkline of that metric over the recent window alongside four detail rows. Every value comes from the live status poll; the history series come from useOverviewHistory.',
      },
    },
  },
  argTypes: {
    status: { description: 'The live Status model from useStatusQuery.' },
    history: { description: 'Rolling percent series and clock labels from useOverviewHistory.' },
    open: { description: 'Which gauge is expanded, or null.' },
    lead: { description: 'Content for the wide first cell — the dashboard passes CoreSummaryStrip.' },
  },
} satisfies Meta<typeof VitalsBand>;

export default meta;
type Story = StoryObj<typeof meta>;

function Band({ initial }: { initial: VitalKey | null }) {
  const [open, setOpen] = useState<VitalKey | null>(initial);
  return (
    <div className="p-root" style={{ padding: 20, background: 'var(--p-bg)' }}>
      <VitalsBand
        status={status}
        history={history}
        open={open}
        onOpen={setOpen}
        lead={<CoreSummaryStrip cores={cores} onOpen={() => undefined} />}
      />
    </div>
  );
}

export const Collapsed: Story = {
  args: { status, history, open: null, onOpen: () => undefined },
  render: () => <Band initial={null} />,
};

export const CpuExpanded: Story = {
  args: { status, history, open: 'cpu', onOpen: () => undefined },
  render: () => <Band initial="cpu" />,
};

export const CoreTiles: Story = {
  args: { status, history, open: null, onOpen: () => undefined },
  render: () => (
    <div className="p-root" style={{ padding: 20, background: 'var(--p-bg)' }}>
      <PanelCard kicker="Cores" icon="xray" pad={12} extra={<Button type="text" size="small">Manage</Button>}>
        <DashboardCores cores={cores} onOpen={() => undefined} />
      </PanelCard>
    </div>
  ),
};
