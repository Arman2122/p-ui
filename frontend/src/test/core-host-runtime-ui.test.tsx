import { fireEvent, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import CoreStatusPopover from '@/layouts/CoreStatusPopover';
import DashboardCores, { CoreSummaryStrip } from '@/pages/index/DashboardCores';
import CoresPage from '@/pages/cores/CoresPage';
import CoreDetailModal from '@/pages/cores/CoreDetailModal';
import { CORE_TRAFFIC_FIXTURES, coreDescriptorFixtures, coreHostRuntimeFixtures, SSH_BUILD_FIXTURE } from '@/pages/cores/CoresPage.fixtures';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

afterEach(() => vi.restoreAllMocks());

function renderRuntime() {
  const cores = coreHostRuntimeFixtures();
  const queryClient = makeTestQueryClient();
  queryClient.setDefaultOptions({ queries: { staleTime: Infinity, retry: false } });
  queryClient.setQueryData(keys.cores.status(), cores);
  queryClient.setQueryData(keys.cores.list(), coreDescriptorFixtures());
  queryClient.setQueryData(keys.inbounds.slim(), CORE_TRAFFIC_FIXTURES);
  return renderWithProviders(<MemoryRouter><CoreStatusPopover onManage={vi.fn()} />
    <CoreSummaryStrip cores={cores} onOpen={vi.fn()} /><DashboardCores cores={cores} onOpen={vi.fn()} /><CoresPage /></MemoryRouter>, { queryClient });
}

describe('host process runtime UI', () => {
  it('keeps seven-running counts and unverified status consistent across the dashboard, Cores page and popover', async () => {
    const { container } = renderRuntime();
    expect(container.querySelector('.ov-core-summary-count')?.textContent).toBe(i18next.t('pages.index.coresRunning', { running: 7, total: 8 }));
    const running = Array.from(container.querySelectorAll<HTMLElement>('.p-strip-c')).find((cell) => cell.textContent?.includes(i18next.t('pages.cores.state.running')))!;
    expect(within(running).getByText('7')).toBeTruthy();
    const external = container.querySelector<HTMLElement>('[data-core-id="mtproto"]')!;
    expect(within(external).getByText(i18next.t('coreRuntime.runningUnverified'))).toBeTruthy();
    expect((within(external).getByRole('button', { name: i18next.t('pages.cores.restart') }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(external).getByRole('button', { name: i18next.t('pages.cores.logs') }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: i18next.t('menu.cores') }));
    const popover = document.querySelector<HTMLElement>('.p-pop')!;
    expect(within(popover).getAllByText(i18next.t('menu.coresRunning', { running: 7, total: 8 }))).toHaveLength(2);
    expect(within(popover).getByText(i18next.t('coreRuntime.healthSummary', { healthy: 4, unverified: 3 }))).toBeTruthy();
  });

  it('shows observed PID/executable ownership even after a failed probe and never loads standalone daemon logs', () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
    const core = coreHostRuntimeFixtures().find((entry) => entry.id === 'openvpn')!;
    core.hostRuntime!.error = 'Permission denied reading another process';
    renderWithProviders(<MemoryRouter><CoreDetailModal core={core} onClose={vi.fn()} onRestart={vi.fn()} restarting={false} onInstalled={vi.fn()} initialTab="runtime" /></MemoryRouter>);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText(i18next.t('pages.cores.state.unknown'))).toBeTruthy();
    expect(within(dialog).getByText('8000')).toBeTruthy();
    expect(within(dialog).getAllByText('/srv/standalone/openvpn')).toHaveLength(6);
    expect(within(dialog).getAllByText(i18next.t('coreRuntime.otherProcess'))).toHaveLength(6);
    expect(within(dialog).getByText('Permission denied reading another process')).toBeTruthy();
    expect(within(dialog).getByText(i18next.t('coreRuntime.panelBinaryVersion'))).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.logging') }));
    expect(within(dialog).getByText(i18next.t('coreRuntime.noManagedActions'))).toBeTruthy();
    expect(get.mock.calls.some(([url]) => url.includes('/logs'))).toBe(false);
  });

  it('labels panel-owned PIDs separately from other observed executables', () => {
    const core = coreHostRuntimeFixtures().find((entry) => entry.id === 'openvpn')!;
    core.instances = [{ instanceId: 9, tag: 'panel-openvpn', pid: 8000, state: 'running', ready: false }];
    renderWithProviders(<MemoryRouter><CoreDetailModal core={core} onClose={vi.fn()} onRestart={vi.fn()} restarting={false} onInstalled={vi.fn()} initialTab="runtime" /></MemoryRouter>);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getAllByText(i18next.t('coreRuntime.managedProcess'))).toHaveLength(1);
    expect(within(dialog).getAllByText(i18next.t('coreRuntime.otherProcess'))).toHaveLength(5);
  });

  it('shows the complete SSH build with panel updates and no independent version requests or install controls', () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', []));
    const core = coreHostRuntimeFixtures().find((entry) => entry.id === 'ssh')!;
    renderWithProviders(<MemoryRouter><CoreDetailModal core={core} onClose={vi.fn()} onRestart={vi.fn()} restarting={false} onInstalled={vi.fn()} initialTab="version" /></MemoryRouter>);
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText(SSH_BUILD_FIXTURE)).toBeTruthy();
    expect(within(dialog).getByText(i18next.t('pages.cores.builtInMaintenance'))).toBeTruthy();
    expect(within(dialog).getByText(i18next.t('coreRuntime.sshBuild'))).toBeTruthy();
    expect(within(dialog).queryByRole('radio')).toBeNull();
    expect(get.mock.calls.some(([url]) => url.includes('/versions'))).toBe(false);
  });
});
