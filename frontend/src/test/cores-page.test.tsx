import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import CoresPage from '@/pages/cores/CoresPage';
import { corePageFixtures, coreDescriptorFixtures, CORE_INBOUND_FIXTURES } from '@/pages/cores/CoresPage.fixtures';
import DashboardCores from '@/pages/index/DashboardCores';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

function Location() { const location = useLocation(); return <output data-testid="core-location">{location.pathname}{location.search}</output>; }

function renderCores(cores = corePageFixtures()) {
  const client = makeTestQueryClient();
  client.setQueryDefaults(keys.cores.status(), { staleTime: Infinity });
  client.setQueryData(keys.cores.status(), cores);
  client.setQueryData(keys.cores.list(), coreDescriptorFixtures());
  client.setQueryData(keys.inbounds.options(), CORE_INBOUND_FIXTURES);
  return renderWithProviders(<MemoryRouter><CoresPage /><Location /></MemoryRouter>, { queryClient: client });
}

describe('Cores page metadata', () => {
  it('labels the built-in listener, core-wide process, kernel interfaces and unavailable core honestly', () => {
    const { container } = renderCores();
    const rows = container.querySelectorAll('[data-core-id]');
    expect(rows).toHaveLength(8);
    const ssh = container.querySelector('[data-core-id="ssh"]')! as HTMLElement;
    const xray = container.querySelector('[data-core-id="xray"]')! as HTMLElement;
    const kernel = container.querySelector('[data-core-id="wgkernel"]')! as HTMLElement;
    const unavailable = container.querySelector('[data-core-id="mtproto"]')! as HTMLElement;
    expect(within(ssh).getByText(i18next.t('pages.cores.builtIn'))).toBeTruthy();
    expect(within(ssh).queryByText(i18next.t('pages.cores.notInstalled'))).toBeNull();
    expect(within(ssh).getByText(i18next.t('pages.cores.listeners'))).toBeTruthy();
    expect(within(ssh).queryByRole('button', { name: i18next.t('pages.cores.versions') })).toBeNull();
    expect(within(xray).getByText('4501')).toBeTruthy();
    expect(within(xray).queryByText(i18next.t('pages.cores.columns.instances'))).toBeNull();
    expect(within(kernel).getByText(i18next.t('pages.cores.interfaces'))).toBeTruthy();
    expect(within(kernel).getByText('1')).toBeTruthy();
    expect(within(unavailable).getByText(i18next.t('pages.cores.unavailable'))).toBeTruthy();
    expect(within(unavailable).queryByText(i18next.t('pages.cores.state.idle'))).toBeNull();
    expect((within(unavailable).getByRole('button', { name: i18next.t('pages.cores.restart') }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(unavailable).getByRole('button', { name: i18next.t('pages.cores.versions') }) as HTMLButtonElement).disabled).toBe(false);
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
    expect(within(row).getByText(i18next.t('pages.cores.coreWide'))).toBeTruthy();
    expect(within(row).getByText(i18next.t('pages.cores.versionUnavailable'))).toBeTruthy();
    expect(within(row).queryByText(i18next.t('pages.cores.notInstalled'))).toBeNull();
    expect(within(row).getByText(i18next.t('pages.cores.state.running'))).toBeTruthy();
  });

  it('shows an empty release catalogue without inventing an available upgrade', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', { installed: '1.0.1', manageable: true, available: [] }));
    const { container } = renderCores();
    const row = container.querySelector('[data-core-id="mtproto"]')! as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: i18next.t('pages.cores.versions') }));
    await screen.findByText(i18next.t('pages.cores.noVersions'));
    expect(get).toHaveBeenCalledWith('/panel/api/cores/mtproto/versions', undefined, expect.anything());
    expect(screen.queryByRole('radio')).toBeNull();
  });

  it('preserves the explicit restart confirmation', async () => {
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, ''));
    const { container } = renderCores();
    const xray = container.querySelector('[data-core-id="xray"]')! as HTMLElement;
    fireEvent.click(within(xray).getByRole('button', { name: i18next.t('pages.cores.restart') }));
    expect(post).not.toHaveBeenCalled();
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: i18next.t('pages.cores.restart') }));
    await waitFor(() => expect(post).toHaveBeenCalledWith('/panel/api/cores/xray/restart'));
  });

  it('shares dashboard glyphs and keeps unavailable and built-in labels there too', () => {
    renderWithProviders(<DashboardCores cores={corePageFixtures()} onOpen={vi.fn()} />);
    const ssh = screen.getByRole('button', { name: /SSH/ });
    const mtproto = screen.getByRole('button', { name: /MTProto/ });
    expect(ssh.textContent).toContain(i18next.t('pages.cores.builtIn'));
    expect(mtproto.textContent).toContain(i18next.t('pages.cores.unavailable'));
    expect(ssh.querySelector('svg')?.innerHTML).not.toBe(screen.getByRole('button', { name: /Xray/ }).querySelector('svg')?.innerHTML);
  });
});
