import type { Meta, StoryObj } from '@storybook/react-vite';
import { Input, InputNumber, Switch } from 'antd';
import { expect, userEvent, within } from 'storybook/test';

import SettingListItem from './SettingListItem';
import SettingsGroup from '@/pages/settings/SettingsGroup';
import '@/pages/settings/SettingsPage.css';

const meta = {
  title: 'UI/SettingListItem',
  component: SettingListItem,
  tags: ['autodocs'],
  parameters: {
    layout: 'padded',
    docs: {
      description: {
        component:
          'Two-column settings row: a title and description on the left, and a control (Switch, InputNumber, …) on the right. Associates the title with the control via `aria-labelledby` for accessibility.',
      },
    },
  },
  argTypes: {
    title: { description: 'Setting name shown on the left.' },
    description: { description: 'Secondary help text under the title.' },
    control: { description: 'The control rendered on the right (Switch, InputNumber, …).' },
    children: { description: 'Alternative to `control`; used when no explicit control is passed.' },
    paddings: { description: 'Row density: `default` or the tighter `small`.' },
  },
} satisfies Meta<typeof SettingListItem>;

export default meta;

type Story = StoryObj<typeof meta>;

export const WithSwitch: Story = {
  args: {
    title: 'Enable subscription',
    description: 'Expose an aggregated subscription URL for this client.',
    control: <Switch defaultChecked />,
  },
};

export const WithNumber: Story = {
  args: {
    title: 'Traffic limit',
    description: 'Cap total traffic in gigabytes. Zero means unlimited.',
    control: <InputNumber min={0} defaultValue={100} style={{ width: '100%' }} />,
  },
};

export const CompactPadding: Story = {
  args: {
    paddings: 'small',
    title: 'Auto renew',
    description: 'Restart the quota window automatically on depletion.',
    control: <Switch />,
  },
};

function SettingsCardPreview({ width = 980, collapsible = false }: { width?: number; collapsible?: boolean }) {
  return <div className="settings-page" style={{ maxWidth: width }}>
    <div className="settings-content">
      <SettingsGroup title="Listener and subscription URLs" collapsible={collapsible}>
        <SettingListItem title="Enable subscription" description="Serve subscription links for your clients." control={<Switch defaultChecked />} />
        <SettingListItem title="Listen domain" description="The hostname used by the subscription listener." control={<Input defaultValue="subscriptions.example.net" />} />
        <SettingListItem title="Listen port" description="Changes take effect after restarting the panel." control={<InputNumber defaultValue={2096} min={1} max={65535} />} />
        <SettingListItem title="Subscription URL" description="The public address clients use to refresh their subscriptions." control={<Input defaultValue="https://subscriptions.example.net/sub/" />} />
      </SettingsGroup>
    </div>
  </div>;
}

export const SettingsCard: Story = {
  render: () => <SettingsCardPreview />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole('textbox', { name: 'Listen domain' });
    const label = canvas.getByText('Listen domain');
    expect(input.getBoundingClientRect().left).toBeGreaterThan(label.getBoundingClientRect().right);
    expect(input.getBoundingClientRect().width).toBeLessThanOrEqual(400);
    await userEvent.clear(input);
    await userEvent.type(input, 'edited.example.net');
    expect(input).toHaveValue('edited.example.net');
  },
  parameters: { docs: { description: { story: 'Settings-scoped responsive card layout. Existing row consumers outside .settings-page retain their layout and padding.' } } },
};

export const SettingsCardNarrow: Story = {
  render: () => <SettingsCardPreview width={360} />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const input = canvas.getByRole('textbox', { name: 'Listen domain' });
    const label = canvas.getByText('Listen domain');
    const card = canvas.getByRole('region', { name: 'Listener and subscription URLs' });
    expect(input.getBoundingClientRect().top).toBeGreaterThan(label.getBoundingClientRect().bottom);
    expect(card.scrollWidth).toBeLessThanOrEqual(card.clientWidth);
    const toggle = canvas.getByRole('switch', { name: 'Enable subscription' });
    await userEvent.click(toggle);
    expect(toggle).not.toBeChecked();
  },
};

export const SettingsCardAdvanced: Story = {
  render: () => <SettingsCardPreview width={360} collapsible />,
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    expect(canvas.getByRole('textbox', { name: 'Listen domain' })).not.toBeVisible();
    const summary = canvas.getByText('Listener and subscription URLs').closest('summary')!;
    await userEvent.click(summary);
    const input = canvas.getByRole('textbox', { name: 'Listen domain' });
    expect(input).toBeVisible();
    await userEvent.clear(input);
    await userEvent.type(input, 'edited.example.net');
    await userEvent.click(summary);
    expect(canvas.getByRole('textbox', { name: 'Listen domain' })).not.toBeVisible();
    await userEvent.click(summary);
    expect(canvas.getByRole('textbox', { name: 'Listen domain' })).toHaveValue('edited.example.net');
  },
};
