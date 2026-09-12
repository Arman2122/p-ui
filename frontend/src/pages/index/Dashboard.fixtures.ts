import { Status } from '@/models/status';
import type { CoreStatusView } from '@/generated/zod';
import type { OverviewHistory } from './useOverviewHistory';

const GiB = 1024 ** 3;
export const dashboardStatus = new Status({
  cpu: 2.2, cpuCores: 2, logicalPro: 2, cpuSpeedMhz: 2100, loads: [0.18, 0.24, 0.21],
  mem: { current: 0.82 * GiB, total: 3.73 * GiB }, swap: { current: 0, total: 0 }, disk: { current: 17.15 * GiB, total: 37.21 * GiB },
  netIO: { up: 18860, down: 7220 }, netTraffic: { sent: 2.91 * GiB, recv: 9.5 * GiB }, tcpCount: 110, udpCount: 15,
  uptime: 25 * 86400, appStats: { threads: 45, mem: 67.96 * 1024 * 1024, uptime: 160000 },
  publicIP: { ipv4: '192.0.2.10', ipv6: '2001:db8::10' }, xray: { state: 'running', version: '26.7.28', errorMsg: '' },
});

const wave = (base: number, amp: number, seed: number) => Array.from({ length: 60 }, (_, i) => Math.max(0, base + Math.sin(i / 5 + seed) * amp * 0.7 + ((i * seed) % 9) * amp * 0.04));
export const dashboardHistory: OverviewHistory = {
  times: Array.from({ length: 60 }, (_, i) => Date.UTC(2026, 8, 13, 12) / 1000 + i * 60),
  labels: Array.from({ length: 60 }, (_, i) => `12:${String(i).padStart(2, '0')}`),
  series: { cpu: wave(2.4, 2.2, 7), mem: wave(21.4, 1.1, 3), swap: wave(0, 0, 5), diskUsage: wave(46, 0.4, 11),
    netUp: wave(210000, 120000, 13), netDown: wave(96000, 64000, 17), tcpCount: wave(110, 12, 19).map(Math.round), udpCount: wave(15, 4, 23).map(Math.round) },
};

export const dashboardCores: CoreStatusView[] = ['xray', 'singbox', 'wgkernel', 'awgkernel', 'ssh', 'mtproto', 'openvpn', 'ocserv'].map((id, i) => ({
  id, titleKey: `cores.${id}.title`, state: i < 4 ? 'running' : 'idle', ready: i < 4, available: true, logs: true, instances: [],
  version: { manageable: id === 'xray', reported: id !== 'ssh', ...(id === 'ssh' ? {} : { installed: id === 'xray' ? '26.7.28' : '1.0.0' }) },
}));
