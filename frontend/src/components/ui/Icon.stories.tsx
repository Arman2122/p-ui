import type { Meta, StoryObj } from '@storybook/react-vite';

import { Icon, iconNames } from './Icon';

const meta = {
  title: 'UI/Icon',
  component: Icon,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          "The panel's own 24x24 stroke icon set, from the Penhoon Panel design. Strokes inherit `currentColor`, so an icon takes the colour of the element around it. Ant Design's icon package stays in use where a control already ships one.",
      },
    },
  },
  argTypes: {
    name: { description: 'Which glyph to draw.', options: iconNames, control: { type: 'select' } },
    size: { description: 'Width and height in pixels.' },
    style: { description: 'Inline styles merged onto the <svg>.' },
  },
} satisfies Meta<typeof Icon>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Single: Story = {
  args: { name: 'gauge', size: 24 },
};

export const Sizes: Story = {
  args: { name: 'shield' },
  render: () => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 16, color: 'var(--p-text)' }}>
      {[12, 14, 16, 20, 24, 32].map((size) => (
        <Icon key={size} name="shield" size={size} />
      ))}
    </div>
  ),
};

export const InheritsColour: Story = {
  args: { name: 'bolt' },
  render: () => (
    <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
      {['var(--p-primary)', 'var(--p-success)', 'var(--p-warning)', 'var(--p-error)', 'var(--p-text-3)'].map((color) => (
        <span key={color} style={{ color, display: 'grid' }}><Icon name="bolt" size={22} /></span>
      ))}
    </div>
  ),
};

export const EveryGlyph: Story = {
  args: { name: 'gauge' },
  render: () => (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(92px, 1fr))', gap: 10 }}>
      {iconNames.map((name) => (
        <div
          key={name}
          style={{
            display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 7,
            padding: '12px 6px', border: '1px solid var(--p-border)', borderRadius: 9, color: 'var(--p-text)',
          }}
        >
          <Icon name={name} size={20} />
          <span style={{ fontSize: 10.5, color: 'var(--p-text-3)', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '100%' }}>
            {name}
          </span>
        </div>
      ))}
    </div>
  ),
};
