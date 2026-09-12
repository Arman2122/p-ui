import type { Meta, StoryObj } from '@storybook/react-vite';
import { MemoryRouter } from 'react-router';
import { fn } from 'storybook/test';
import NodeFleet from './NodeFleet';
import '@/styles/design-pages.css';

const meta = {
  title: 'Pages/Nodes', component: NodeFleet,
  decorators: [(Story) => <MemoryRouter><main className="p-page"><Story /></main></MemoryRouter>],
  args: { nodes: [
    { id: 1, name: 'Helsinki', address: '192.0.2.10', port: 443, scheme: 'https', enable: true, status: 'online', cpuPct: 14.2, memPct: 32, latencyMs: 18, clientCount: 120, inboundCount: 5, uptimeSecs: 86400, xrayState: 'running', xrayVersion: '26.7.28', inboundTags: ['vless-reality', 'wireguard'] },
    { id: 2, name: 'Frankfurt', address: '192.0.2.20', scheme: 'https', port: 443, enable: true, status: 'offline', clientCount: 45, inboundCount: 2, lastError: 'Connection timed out' },
  ], selectedIds: [], onSelectionChange: fn(), onAdd: fn(), onMtls: fn(), onEdit: fn(), onDelete: fn(), onProbe: fn(), onToggleEnable: fn(), onUpdateNode: fn(), onUpdateSelected: fn() },
} satisfies Meta<typeof NodeFleet>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Fleet: Story = {};
