import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { Status } from '@/models/status';
import { StatusSchema } from '@/schemas/status';
import { PanelHostPanel } from '@/pages/index/DashboardPanels';
import { renderWithProviders } from './test-utils';

describe('panel host listener port', () => {
  it.each([
    { title: 'prefers the effective environment override', listenPort: 16119, expected: '16119' },
    { title: 'keeps the saved-port fallback when status omits an override', listenPort: undefined, expected: '2053' },
  ])('$title', ({ listenPort, expected }) => {
    const status = new Status(StatusSchema.parse({ listenPort }));
    renderWithProviders(<PanelHostPanel status={status} webPort={2053} nodeCount={0} basePath="/" sessionMinutes={60} showIp={false} onToggleIp={vi.fn()} />);
    expect(screen.getByText(expected)).toBeTruthy();
    expect(screen.queryByText(expected === '16119' ? '2053' : '16119')).toBeNull();
  });
});
