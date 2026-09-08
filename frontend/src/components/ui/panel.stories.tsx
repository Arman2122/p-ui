import type { Meta, StoryObj } from '@storybook/react-vite';
import { Button, Tag } from 'antd';

import {
  ActionIcons, Delta, EventItem, Field, Gauge, Kicker, LeaderRow, LiveBadge, Meter,
  PageHeader, PanelCard, Ring, ShareRow, StatusChip, StatusDot, StatusPip, Toolbar,
  type PanelStatus,
} from './panel';

const ACTIVE: PanelStatus = { key: 'active', label: 'Active', color: 'var(--p-success)' };
const EXPIRING: PanelStatus = { key: 'expiring', label: 'Expiring', color: 'var(--p-warning)' };
const ENDED: PanelStatus = { key: 'ended', label: 'Ended', color: 'var(--p-error)' };
const DISABLED: PanelStatus = { key: 'disabled', label: 'Disabled', color: '#8b8d94' };

const meta = {
  title: 'UI/Panel primitives',
  component: PanelCard,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'The shared building blocks of the Penhoon Panel design: cards, page headers, meters, gauges and status marks. Every colour comes from a --p-* token published by useTokenBridge, so these follow the panel theme rather than carrying their own palette.',
      },
    },
  },
  argTypes: {
    title: { description: 'Card title, shown when no kicker is given.' },
    kicker: { description: 'Uppercase label that replaces the title when set.' },
    icon: { description: 'Optional glyph shown before the title.' },
    extra: { description: 'Right-hand slot of the card header.' },
    pad: { description: 'Padding of the card body in pixels.' },
  },
} satisfies Meta<typeof PanelCard>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Card: Story = {
  args: {
    title: 'Traffic',
    icon: 'chart',
    extra: <Button size="small">Reset</Button>,
    children: <span style={{ color: 'var(--p-text-2)', fontSize: 12.5 }}>Card body.</span>,
  },
};

export const Header: Story = {
  args: { title: 'x' },
  render: () => (
    <PageHeader
      title="Clients"
      sub="10 clients across 6 inbounds"
      actions={<><Button size="small">Import</Button><Button size="small" type="primary">Add client</Button></>}
    />
  ),
};

export const Statuses: Story = {
  args: { title: 'x' },
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        {[ACTIVE, EXPIRING, ENDED, DISABLED].map((s) => <StatusChip key={s.key} status={s} />)}
        <StatusChip status={ACTIVE} pulse />
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 18 }}>
        {[ACTIVE, EXPIRING, ENDED, DISABLED].map((s) => <StatusPip key={s.key} status={s} note="200 GB plan" />)}
        <StatusPip status={ACTIVE} online note="online now" />
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
        <StatusDot color="var(--p-success)" pulse />
        <LiveBadge />
        <Delta value={12} />
        <Delta value={-4} />
      </div>
    </div>
  ),
};

export const Readouts: Story = {
  args: { title: 'x' },
  render: () => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 28, flexWrap: 'wrap' }}>
      <Gauge value={2.2} unit="%" />
      <Gauge value={46.1} unit="%" tone="var(--p-warning)" />
      <Gauge value={0} muted />
      <Ring value={21.5} label="Memory" sub="821 MB / 3.73 GB" />
    </div>
  ),
};

export const Rows: Story = {
  args: { title: 'x' },
  render: () => (
    <PanelCard title="Top clients" icon="users" pad={14}>
      <LeaderRow rank={1} name="Roham" meta="GAME" value="857 GB" pct={86} tone="var(--p-primary)" />
      <LeaderRow rank={2} name="office-nl" meta="test-awg" value="952 GB" pct={62} tone="var(--p-primary)" />
      <ShareRow label="VLESS-REALITY" tag={<Tag bordered={false}>vless</Tag>} value="1.6 GB" pct={54} tone="var(--p-success)" />
      <ShareRow label="Unmetered" value="140 GB" pct={0} capped={false} />
      <div style={{ marginTop: 14 }}>
        <EventItem time="2 min ago" title="Roham connected" meta="GAME · 1 of 1 IP slots" tone="var(--p-success)" icon="user" />
        <EventItem time="1 h ago" title="Routing rule edited" meta="geosite:openai → warp" tone="var(--p-primary)" icon="routing" last />
      </div>
    </PanelCard>
  ),
};

export const Bits: Story = {
  args: { title: 'x' },
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
      <div><Kicker>Quota</Kicker><Meter pct={72} tone="var(--p-primary)" /></div>
      <div style={{ display: 'flex', gap: 28 }}>
        <Field label="Port" value="51888" sub="UDP" />
        <Field label="Clients" value="3" sub="3 online" />
      </div>
      <Toolbar left={<span className="p-sub">6 inbounds</span>} right={<ActionIcons items={[
        { icon: 'edit', title: 'Edit' },
        { icon: 'copy', title: 'Duplicate' },
        { icon: 'trash', title: 'Delete', danger: true },
      ]} />} />
    </div>
  ),
};
