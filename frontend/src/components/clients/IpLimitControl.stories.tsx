import type { Meta, StoryObj } from '@storybook/react-vite';

import { IpLimitControl } from './IpLimitControl';

const meta = {
  title: 'Clients/IpLimitControl',
  component: IpLimitControl,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          'Picks between an unlimited IP cap and a numeric one. The stored value is still `0` for unlimited, but the operator never types it: a bare `0` in a number field reads as "allow nothing" when it means the opposite. A host that cannot enforce the cap is a third state, shown disabled with the reason.',
      },
    },
  },
  argTypes: {
    value: { description: 'Stored value; `0` means unlimited.' },
    disabled: { description: 'True when this host cannot enforce an IP limit (no Fail2ban).' },
    notice: { description: 'Why the cap cannot be enforced. Shown only when disabled.' },
    addon: { description: 'Rendered beside the number, for the IP-log button.' },
  },
} satisfies Meta<typeof IpLimitControl>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Unlimited: Story = {
  args: { value: 0, onChange: () => {} },
};

export const Capped: Story = {
  args: { value: 2, onChange: () => {} },
};

export const NotEnforceable: Story = {
  args: {
    value: 0,
    onChange: () => {},
    disabled: true,
    notice: 'Fail2ban is not installed, so the IP limit cannot be enforced.',
  },
};
