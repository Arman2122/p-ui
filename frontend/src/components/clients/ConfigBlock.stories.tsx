import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, waitFor } from 'storybook/test';

import ConfigBlock from './ConfigBlock';

const meta = {
  title: 'Clients/ConfigBlock',
  component: ConfigBlock,
  tags: ['autodocs'],
  parameters: {
    layout: 'padded',
    docs: {
      description: {
        component:
          'Collapsible panel that displays a client config or share link with copy, download, and QR-code actions. Used on the clients and inbounds pages to present generated links.',
      },
    },
  },
  argTypes: {
    label: { description: 'Protocol/type badge shown on the panel header (e.g. `vless`, `trojan`).' },
    text: { description: 'The config or share-link text to display, copy, download, and encode as a QR code.' },
    fileName: { description: 'File name used when downloading the text.' },
    qrRemark: { description: 'Optional remark embedded in the QR panel; falls back to `label`.' },
    showQr: { description: 'Whether to show the QR-code action button.' },
    tagColor: { description: 'Ant Design tag color for the header badge.' },
    defaultOpen: { description: 'Whether the panel starts expanded.' },
  },
} satisfies Meta<typeof ConfigBlock>;

export default meta;

type Story = StoryObj<typeof meta>;

const sampleLink = 'vless://11112222-3333-4444-5555-666677778888@panel.example.com:443'
  + '?type=ws&security=tls&path=%2Fpath#example-node';

export const Collapsed: Story = {
  args: { label: 'vless', text: sampleLink, fileName: 'client-config.txt' },
  play: async ({ canvas, userEvent }) => {
    await expect(canvas.queryByText(/vless:\/\/11112222/)).not.toBeInTheDocument();
    await userEvent.click(canvas.getByText('vless'));
    const configText = await canvas.findByText(/vless:\/\/11112222/);
    await waitFor(() => expect(configText).toBeVisible());
    /* Settled, not merely visible. The a11y pass runs after this play function
       and measures rendered colour, so a panel still fading in is scored at its
       blended opacity — #595959 halfway to white reads as #a6a6a6 and fails the
       contrast rule. toBeVisible() is true from the first frame of that fade,
       which is why this only failed on slower machines. */
    await waitFor(() => {
      expect(document.getAnimations().filter((a) => a.playState === 'running')).toHaveLength(0);
    });
    await expect(canvas.getByRole('button', { name: 'Copy' })).toBeVisible();
    await expect(canvas.getByRole('button', { name: 'Download' })).toBeVisible();
    await expect(canvas.getByRole('button', { name: 'QR Code' })).toBeVisible();
  },
};

export const Expanded: Story = {
  args: { label: 'vless', text: sampleLink, fileName: 'client-config.txt', defaultOpen: true },
};

export const WithoutQr: Story = {
  args: { label: 'trojan', text: sampleLink, fileName: 'client-config.txt', showQr: false, tagColor: 'geekblue' },
};
