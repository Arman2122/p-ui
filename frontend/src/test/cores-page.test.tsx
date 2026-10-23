import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { FileManager, HttpUtil, Msg } from '@/utils';
import CoresPage from '@/pages/cores/CoresPage';
import { corePageFixtures, coreDescriptorFixtures, CORE_INBOUND_FIXTURES, CORE_TRAFFIC_FIXTURES } from '@/pages/cores/CoresPage.fixtures';
import { coreMetrics, newerCoreVersion } from '@/pages/cores/useCoreInventory';
import DashboardCores from '@/pages/index/DashboardCores';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

function Location() { const location = useLocation(); return <output data-testid="core-location">{location.pathname}{location.search}</output>; }

function renderCores(cores = corePageFixtures()) {
  const client = makeTestQueryClient();
  client.setQueryDefaults(keys.cores.status(), { staleTime: Infinity });
  client.setQueryDefaults(keys.inbounds.slim(), { staleTime: Infinity });
  client.setQueryData(keys.cores.status(), cores);
  client.setQueryData(keys.cores.list(), coreDescriptorFixtures());
  client.setQueryData(keys.inbounds.options(), CORE_INBOUND_FIXTURES);
  client.setQueryData(keys.inbounds.slim(), CORE_TRAFFIC_FIXTURES, { updatedAt: Date.now() - 1000 });
  return { ...renderWithProviders(<MemoryRouter><CoresPage /><Location /></MemoryRouter>, { queryClient: client }), queryClient: client };
}

describe('Cores page metadata', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', corePageFixtures()));
  });
  it('renders the five-stat reference and compact owned-core metrics without inventing live or historical samples', () => {
    const { container } = renderCores();
    const rows = container.querySelectorAll('[data-core-id]');
    expect(rows).toHaveLength(8);
    const ssh = container.querySelector('[data-core-id="ssh"]')! as HTMLElement;
    const xray = container.querySelector('[data-core-id="xray"]')! as HTMLElement;
    const kernel = container.querySelector('[data-core-id="wgkernel"]')! as HTMLElement;
    const unavailable = container.querySelector('[data-core-id="mtproto"]')! as HTMLElement;
    expect(within(ssh).getByText(i18next.t('pages.cores.builtIn'))).toBeTruthy();
    expect(within(ssh).queryByText(i18next.t('pages.cores.notInstalled'))).toBeNull();
    expect(container.querySelectorAll('.p-strip-c')).toHaveLength(5);
    expect(within(ssh).getByText(i18next.t('menu.inbounds'))).toBeTruthy();
    expect(within(ssh).queryByRole('button', { name: i18next.t('pages.cores.versions') })).toBeNull();
    expect(within(xray).getByText('2.0 GB')).toBeTruthy();
    expect(within(xray).getByText(/3 ·/)).toBeTruthy();
    expect(within(xray).queryByText('4501')).toBeNull();
    expect(within(xray).queryByText(i18next.t('pages.cores.columns.instances'))).toBeNull();
    expect(within(kernel).queryByText(i18next.t('pages.cores.interfaces'))).toBeNull();
    expect(within(kernel).getByText('1')).toBeTruthy();
    expect(within(unavailable).getByText(i18next.t('pages.cores.state.stopped'))).toBeTruthy();
    expect(within(unavailable).getByText(/mtg-proxy 1.0.1 is installed/)).toBeTruthy();
    expect(within(unavailable).queryByText(i18next.t('pages.cores.state.idle'))).toBeNull();
    expect((within(unavailable).getByRole('button', { name: i18next.t('pages.cores.restart') }) as HTMLButtonElement).disabled).toBe(true);
    expect(within(unavailable).getByRole('button', { name: i18next.t('pages.cores.fidelity.more') })).toBeTruthy();
    expect(within(xray).getByRole('img').textContent).toBe('—');
    expect(within(xray).queryByRole('button', { name: i18next.t('pages.cores.columns.detail') })).toBeNull();
    expect(ssh.querySelector('.p-alert-i svg')?.innerHTML).not.toBe(xray.querySelector('.p-alert-i svg')?.innerHTML);
  });

  it('keeps logging, host maintenance, real inbound metadata and configuration validation routes in the detail tabs', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', ['Listener opened']));
    renderCores();
    fireEvent.click(screen.getByRole('button', { name: 'SSH' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.runtime') }));
    expect(within(dialog).getByRole('columnheader', { name: i18next.t('pages.cores.panelProcessId') })).toBeTruthy();
    expect(within(dialog).getByText('ssh-local')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.logging') }));
    await within(dialog).findByText('Listener opened');
    expect(get).toHaveBeenCalledWith('/panel/api/cores/ssh/logs', { instance: 13, n: 200 }, expect.anything());
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('menu.inbounds') }));
    await within(dialog).findByText('Private SSH');
    expect(within(dialog).queryByText('Edge VLESS')).toBeNull();
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.columns.version') }));
    expect(within(dialog).getByText(i18next.t('pages.cores.builtInMaintenance'))).toBeTruthy();
    expect(within(dialog).queryByRole('radio')).toBeNull();
    expect(get.mock.calls.some(([path]) => path === '/panel/api/cores/ssh/versions')).toBe(false);
    fireEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.runtime') }));
    fireEvent.click(within(dialog).getByRole('button', { name: i18next.t('menu.xray') }));
    expect(screen.getByTestId('core-location').textContent).toBe('/xray?core=ssh');
  });

  it('does not turn an unreported process ID or a failed version probe into a missing daemon claim', () => {
    const cores = corePageFixtures();
    cores[0] = { ...cores[0], pid: undefined, version: { reported: true, manageable: true, error: 'Version probe could not read the executable' } };
    const { container } = renderCores(cores);
    const row = container.querySelector('[data-core-id="xray"]')! as HTMLElement;
    expect(within(row).getByText(i18next.t('pages.cores.versionUnavailable'))).toBeTruthy();
    expect(within(row).queryByText(i18next.t('pages.cores.notInstalled'))).toBeNull();
    expect(within(row).getByText(i18next.t('pages.cores.state.running'))).toBeTruthy();
  });

  it('shows an empty release catalogue without inventing an available upgrade', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', { installed: '1.0.1', manageable: true, available: [] }));
    const { container } = renderCores();
    const row = container.querySelector('[data-core-id="mtproto"]')! as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: i18next.t('pages.cores.fidelity.more') }));
    fireEvent.click(await screen.findByRole('menuitem', { name: i18next.t('pages.cores.versions') }));
    await screen.findByText(i18next.t('pages.cores.noVersions'));
    expect(get).toHaveBeenCalledWith('/panel/api/cores/mtproto/versions', undefined, expect.anything());
    expect(within(screen.getByRole('dialog')).queryByRole('radio')).toBeNull();
  });

  it('preserves the explicit restart confirmation', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, ''));
    const { container } = renderCores();
    const xray = container.querySelector('[data-core-id="xray"]')! as HTMLElement;
    fireEvent.click(within(xray).getByRole('button', { name: i18next.t('pages.cores.restart') }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: i18next.t('pages.cores.restart') }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/cores/xray/restart', undefined, expect.anything()));
  });

  it('opens the selected cores configuration from the compact Config action', () => {
    const { container } = renderCores();
    fireEvent.click(within(container.querySelector('[data-core-id="xray"]')! as HTMLElement).getByRole('button', { name: i18next.t('pages.cores.fidelity.config') }));
    expect(screen.getByTestId('core-location').textContent).toBe('/xray?core=xray');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('switches all eight cores between cards and rows and exports only real status and aggregate data', () => {
    vi.spyOn(Date, 'now').mockReturnValue(1_800_000_000_000);
    const download = vi.spyOn(FileManager, 'downloadTextFile').mockImplementation(() => undefined);
    const { container } = renderCores();
    fireEvent.click(screen.getByRole('radio', { name: i18next.t('pages.cores.fidelity.cards') }));
    expect(container.querySelectorAll('.core-card')).toHaveLength(8);
    expect(container.querySelectorAll('.p-corerow')).toHaveLength(0);
    fireEvent.click(screen.getByRole('radio', { name: i18next.t('pages.cores.fidelity.rows') }));
    expect(container.querySelectorAll('.p-corerow')).toHaveLength(8);
    fireEvent.click(screen.getByRole('button', { name: i18next.t('pages.cores.fidelity.exportStatus') }));
    const exported = JSON.parse(String(download.mock.calls[0][0]));
    expect(exported.cores).toHaveLength(8);
    expect(exported.cores[0]).toMatchObject({ id: 'xray', pid: 4501, inboundCount: 2, clientCount: 3, trafficBytes: 2149580800, liveClients: null, trafficHistory: null });
    expect(download.mock.calls[0][0]).not.toContain('shared-customer');
  });

  it('checks real version endpoints manually and shows only a confirmed newer stable release', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      const obj = url.endsWith('/status') ? corePageFixtures() : { manageable: true, installed: url.includes('singbox') ? '1.12.0' : '26.7.28', available: url.includes('singbox') ? ['v1.13.0', 'v1.11.0'] : ['v26.7.28'] };
      return new Msg(true, '', obj as T);
    });
    const { container } = renderCores();
    expect(get).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: i18next.t('pages.cores.fidelity.checkVersions') }));
    await within(container.querySelector('[data-core-id="singbox"]')! as HTMLElement).findByText('v1.13.0');
    expect(get.mock.calls.filter(([url]) => url.endsWith('/versions')).map(([url]) => url).sort()).toEqual(['/panel/api/cores/mtproto/versions', '/panel/api/cores/singbox/versions', '/panel/api/cores/xray/versions']);
  });

  it('restarts available managed runtimes only after confirmation and reports partial failures', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string) { return new Msg<T>(!url.includes('ssh'), url.includes('ssh') ? 'Listener busy' : ''); });
    renderCores();
    fireEvent.click(screen.getByRole('button', { name: i18next.t('pages.cores.fidelity.restartAll') }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: i18next.t('pages.cores.fidelity.restartAll') }));
    await screen.findByText('SSH: Listener busy');
    expect(post.mock.calls.map(([url]) => url)).toEqual(['xray', 'singbox', 'wgkernel', 'ssh', 'openvpn', 'ocserv'].map((id) => `/panel/api/cores/${id}/restart`));
  });

  it('maps protocols through the descriptor, excludes remote consumption and deduplicates a shared client', () => {
    const descriptor = coreDescriptorFixtures()[0];
    const metrics = coreMetrics({ ...descriptor, id: 'renamed-core' }, CORE_TRAFFIC_FIXTURES);
    expect(metrics.inbounds?.map((row) => row.id)).toEqual([10, 16]);
    expect(metrics.clients).toBe(3);
    expect(metrics.traffic).toBe(2149580800);
    expect(coreMetrics(descriptor, undefined).traffic).toBeUndefined();
    expect(coreMetrics(descriptor, [{ id: 1, protocol: 'vless' }])).toMatchObject({ clients: undefined, traffic: undefined });
  });

  it('does not equate an older release or an uncomparable development version with an update', () => {
    const core = corePageFixtures()[0];
    expect(newerCoreVersion(core, { manageable: true, installed: '26.7.28', available: ['v26.6.1'] })).toBeNull();
    expect(newerCoreVersion(core, { manageable: true, installed: 'dev-2af4639', available: ['v26.7.28'] })).toBeUndefined();
    expect(newerCoreVersion(core, undefined)).toBeUndefined();
  });

  it('plots only fresh local counter observations and breaks on an individual inbound reset', async () => {
    const { container, queryClient } = renderCores();
    const firstAt = Date.now() + 100;
    const readings = (increment: number, reset = false) => CORE_TRAFFIC_FIXTURES.map((row) => ({ ...row,
      up: row.id === 10 && reset ? 0 : row.up + increment, down: row.down + increment,
    }));
    const publish = async (at: number, increment: number, reset = false) => {
      await act(async () => {
        queryClient.setQueryData(keys.inbounds.slim(), readings(increment, reset), { updatedAt: at });
        await new Promise((resolve) => setTimeout(resolve, 0));
      });
    };
    const chart = () => container.querySelector('[data-core-id="xray"] .live-traffic-trend')!;
    expect(chart().querySelector('svg')).toBeNull();
    await publish(firstAt, 0);
    await publish(firstAt + 10000, 100);
    expect(chart().querySelector('svg')).toBeNull();
    await publish(firstAt + 20000, 250);
    expect(chart().querySelector('svg path')).toBeTruthy();
    await publish(firstAt + 30000, 2000000, true);
    expect(chart().querySelector('svg')).toBeNull();
    fireEvent.click(screen.getByRole('radio', { name: i18next.t('pages.cores.fidelity.cards') }));
    expect(container.querySelector('[data-core-id="xray"] .live-traffic-trend svg')).toBeNull();
  });

  it('shares dashboard glyphs and keeps unavailable and built-in labels there too', () => {
    renderWithProviders(<DashboardCores cores={corePageFixtures()} onOpen={vi.fn()} />);
    const ssh = screen.getByRole('button', { name: /SSH/ });
    const mtproto = screen.getByRole('button', { name: /MTProto/ });
    expect(ssh.textContent).toContain(i18next.t('pages.cores.builtIn'));
    expect(mtproto.textContent).toContain(i18next.t('pages.cores.state.stopped'));
    expect(ssh.querySelector('svg')?.innerHTML).not.toBe(screen.getByRole('button', { name: /Xray/ }).querySelector('svg')?.innerHTML);
  });
});
