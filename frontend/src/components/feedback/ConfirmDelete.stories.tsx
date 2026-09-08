import type { Meta, StoryObj } from '@storybook/react-vite';

import ConfirmDelete from './ConfirmDelete';
import '@/styles/panel.css';

const meta = {
  title: 'Feedback/ConfirmDelete',
  component: ConfirmDelete,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'A destructive confirmation that spells out the blast radius rather than asking "are you sure?". The typed word is reserved for sweeps big enough that a mis-click is expensive — asking for it on every single delete just trains people to type it without reading.',
      },
    },
  },
  argTypes: {
    title: { description: 'What is about to happen, as a question.' },
    what: { description: 'One line saying what the action actually touches.' },
    lines: { description: 'Consequences, one bullet each. Say what breaks.' },
    requireText: { description: 'When set, the confirm button waits for this word.' },
    danger: { description: 'Red for destructive, amber for merely disruptive.' },
  },
} satisfies Meta<typeof ConfirmDelete>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SingleClient: Story = {
  args: {
    open: true,
    title: 'Delete kiosk-07?',
    what: 'This cannot be undone.',
    lines: [
      'Its credentials stop working immediately.',
      'Usage history is removed.',
      'Any subscription link it holds returns 404.',
    ],
    okText: 'Delete client',
    onCancel: () => undefined,
    onConfirm: () => undefined,
  },
};

export const BulkSweepNeedsTyping: Story = {
  args: {
    open: true,
    title: 'Reset traffic for all 10 clients?',
    what: 'Counters go to zero; quotas, expiry and credentials are untouched.',
    lines: [
      '1.2 TB of recorded usage is cleared.',
      'Clients disabled by depletion become active again.',
      'Usage history in the charts is not affected.',
    ],
    requireText: 'RESET',
    okText: 'Reset all counters',
    danger: false,
    onCancel: () => undefined,
    onConfirm: () => undefined,
  },
};
