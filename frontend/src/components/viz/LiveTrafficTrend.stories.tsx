import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import LiveTrafficTrend from './LiveTrafficTrend';

const meta = {
  title: 'Viz/LiveTrafficTrend', component: LiveTrafficTrend, tags: ['autodocs'],
  parameters: { layout: 'padded', docs: { description: { component: 'Small SVG rate history from fresh cumulative-counter observations. These stories contain illustrative samples only; production history is collected after the page opens.' } } },
  args: { width: 180, height: 50, showCaption: true },
} satisfies Meta<typeof LiveTrafficTrend>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Waiting: Story = {
  args: { series: { observedAt: 1000, status: 'waiting', points: [{ at: 1000, bytesPerSecond: null }] } },
  play: async ({ canvasElement }) => { await expect(within(canvasElement).getByRole('img')).toHaveTextContent('—'); },
};
export const Measured: Story = {
  args: { series: { observedAt: 31000, status: 'ready', points: [{ at: 1000, bytesPerSecond: null }, { at: 11000, bytesPerSecond: 1024 }, { at: 21000, bytesPerSecond: 2048 }, { at: 31000, bytesPerSecond: 512 }] } },
  play: async ({ canvasElement }) => { await expect(canvasElement.querySelectorAll('svg path')).toHaveLength(1); },
};
export const ResetGap: Story = {
  args: { series: { observedAt: 51000, status: 'ready', points: [{ at: 1000, bytesPerSecond: 2048 }, { at: 11000, bytesPerSecond: 1024 }, { at: 21000, bytesPerSecond: null }, { at: 41000, bytesPerSecond: 2048 }, { at: 51000, bytesPerSecond: 3072 }] } },
  play: async ({ canvasElement }) => { await expect(canvasElement.querySelectorAll('svg path')).toHaveLength(2); },
};
export const ObservedZero: Story = {
  args: { series: { observedAt: 21000, status: 'ready', points: [{ at: 1000, bytesPerSecond: null }, { at: 11000, bytesPerSecond: 0 }, { at: 21000, bytesPerSecond: 0 }] } },
};
