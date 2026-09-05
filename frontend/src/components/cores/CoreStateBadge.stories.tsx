import type { Meta, StoryObj } from '@storybook/react-vite';

import { CoreStateBadge } from './CoreStateBadge';

const meta = {
  title: 'Cores/CoreStateBadge',
  component: CoreStateBadge,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'The one badge the Cores page shows for a daemon: its supervisor state and whether it answers on its control surface, read together. A process with a pid and no listener is amber, not green; a failed unit carries its last words on hover.',
      },
    },
  },
  argTypes: {
    state: { description: 'running, degraded, stopped, failed, idle or unknown.' },
    ready: { description: 'The daemon answers on its control surface.' },
    detail: { description: 'Why it is not ready, or the last exit; shown on hover.' },
  },
} satisfies Meta<typeof CoreStateBadge>;

export default meta;

type Story = StoryObj<typeof meta>;

export const RunningAndReady: Story = { args: { state: 'running', ready: true } };
export const RunningNotAnswering: Story = { args: { state: 'running', ready: false, detail: 'the management socket is not open yet' } };
export const Degraded: Story = { args: { state: 'degraded', ready: false } };
export const Failed: Story = { args: { state: 'failed', ready: false, detail: 'signal: killed: 2026-09-05T18:33:49+0000 ocserv[1234]: bind failed' } };
export const Idle: Story = { args: { state: 'idle', ready: true } };
export const Unknown: Story = { args: { state: 'unknown', ready: false } };
