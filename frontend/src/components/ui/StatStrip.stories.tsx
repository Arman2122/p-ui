import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';

import StatStrip, { type StatStripItem } from './StatStrip';
import '@/styles/panel.css';

const meta = {
  title: 'UI/StatStrip',
  component: StatStrip,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'The row of counts above a table. Given `onPick`, each cell doubles as the filter for that bucket — so the summary and the filter are one control and cannot disagree with each other. Without it the strip is read-only.',
      },
    },
  },
  argTypes: {
    items: { description: 'One cell per bucket. `color` adds a status dot; `pulse` makes it breathe.' },
    active: { description: 'Key of the selected cell.' },
    onPick: { description: 'When set, cells become filter buttons.' },
  },
} satisfies Meta<typeof StatStrip>;

export default meta;
type Story = StoryObj<typeof meta>;

const items = [
  { key: 'all', label: 'All clients', value: 10 },
  { key: 'online', label: 'Online now', value: 4, color: 'var(--p-primary)', pulse: true },
  { key: 'active', label: 'Active', value: 6, color: 'var(--p-success)' },
  { key: 'expiring', label: 'Expiring / ended', value: 2, color: 'var(--p-warning)' },
  { key: 'depleting', label: 'Depleting', value: 1, color: 'var(--p-warning)' },
  { key: 'disabled', label: 'Disabled', value: 1, color: '#8b8d94' },
];

function Filterable({ items }: { items: StatStripItem[] }) {
  const [active, setActive] = useState('all');
  return (
    <div className="p-root" style={{ background: 'var(--p-bg)', padding: 16 }}>
      <StatStrip items={items} active={active} onPick={setActive} />
    </div>
  );
}

export const AsAFilter: Story = {
  args: { items, active: 'all' },
  render: (args) => <Filterable items={args.items} />,
};

export const ReadOnly: Story = {
  args: { items: items.slice(0, 4) },
  render: (args) => (
    <div className="p-root" style={{ background: 'var(--p-bg)', padding: 16 }}>
      <StatStrip items={args.items} />
    </div>
  ),
};

export const Few: Story = {
  args: { items: items.slice(0, 3) },
  render: (args) => (
    <div className="p-root" style={{ background: 'var(--p-bg)', padding: 16 }}>
      <StatStrip items={args.items} />
    </div>
  ),
};
