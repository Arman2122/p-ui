import type { Meta, StoryObj } from '@storybook/react-vite';

import { BansTable } from './BansTable';

const until = Math.floor(Date.now() / 1000) + 25 * 60;

const meta = {
  title: 'Clients/BansTable',
  component: BansTable,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'Every device-cap ban this panel decided: a device over its cap dropped on the port of its inbound, an address the operator banned on every inbound, or a shared address reported and left alone. Held on this host or on the named node, until the deadline the firewall itself enforces.',
      },
    },
  },
  argTypes: {
    bans: { description: 'Rows as the panel lists them, soonest to lapse first.' },
    onRelease: { description: 'Frees one (client, source) now; absent, the table is read-only.' },
    onReleaseAll: { description: 'Frees every ban; absent, the button is hidden.' },
  },
} satisfies Meta<typeof BansTable>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Mixed: Story = {
  args: {
    bans: [
      { id: 1, clientEmail: 'alice', source: '203.0.113.7', ports: '443,8443', reason: 'over-cap', node: '', origin: '', createdAt: until - 300, until },
      { id: 2, clientEmail: 'bob', source: '198.51.100.9', ports: '2053', reason: 'over-cap', node: 'de-fra-1', origin: '', createdAt: until - 300, until },
      { id: 3, clientEmail: '', source: '192.0.2.44', ports: '*', reason: 'operator', node: '', origin: '', createdAt: until - 300, until: until + 3600 },
      { id: 4, clientEmail: 'carol', source: '203.0.113.7', ports: '', reason: 'shared-skipped', node: '', origin: '', createdAt: until - 300, until },
    ],
    onRelease: () => {},
    onReleaseAll: () => {},
  },
};

export const Empty: Story = {
  args: { bans: [], onRelease: () => {}, onReleaseAll: () => {} },
};

export const ReadOnly: Story = {
  args: {
    bans: [{ id: 1, clientEmail: 'alice', source: '203.0.113.7', ports: '443', reason: 'over-cap', node: '', origin: '', createdAt: until - 300, until }],
  },
};
