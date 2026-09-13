import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import i18next from 'i18next';
import type { CoreStatusView } from '@/generated/zod';
import { HttpUtil, Msg } from '@/utils';
import { CoreVersionsPanel } from '@/pages/cores/CoreVersionsModal';
import { corePageFixtures } from '@/pages/cores/CoresPage.fixtures';
import { renderWithProviders } from './test-utils';

afterEach(() => vi.restoreAllMocks());
const versions = { manageable: true, installed: '1.12.0', available: ['v1.12.0', 'v1.13.0'] };
const core = (): CoreStatusView => corePageFixtures().find((item) => item.id === 'singbox')!;
const installLabel = (version: string) => i18next.t('pages.cores.installConfirm', { core: 'sing-box', version });

async function confirmInstall(version = 'v1.13.0') {
  fireEvent.click(await screen.findByLabelText(installLabel(version)));
  const dialog = screen.getByRole('dialog');
  expect(dialog.textContent).toContain(installLabel(version));
  fireEvent.click(within(dialog).getByRole('button', { name: i18next.t('pages.cores.install') }));
}

describe('core version installation', () => {
  it('confirms the chosen release, reports a disk install and leaves restart as a separate explicit action', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValueOnce(new Msg(true, '', versions)).mockResolvedValue(new Msg(true, '', { ...versions, installed: '1.13.0' }));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'installed'));
    const onInstalled = vi.fn(), onRestart = vi.fn();
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={onInstalled} onRestart={onRestart} />);
    await confirmInstall();
    await waitFor(() => expect(onInstalled).toHaveBeenCalledTimes(1));
    expect(post).toHaveBeenCalledWith('/panel/api/cores/singbox/install/v1.13.0');
    expect(onRestart).not.toHaveBeenCalled();
    await screen.findByText(i18next.t('coreUpdates.installSucceeded', { version: 'v1.13.0' }));
    await screen.findByText(i18next.t('coreUpdates.installedOnDisk', { version: '1.13.0' }));
    fireEvent.click(screen.getByRole('button', { name: i18next.t('coreUpdates.restartManaged') }));
    expect(onRestart).toHaveBeenCalledTimes(1);
    expect(post.mock.calls).toHaveLength(1);
  });

  it('offers the release list when the binary is missing, which is how a missing daemon gets installed', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', { manageable: true, available: ['v1.9.0'], error: 'core: daemon not installed' }));
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={vi.fn()} />);
    await screen.findByLabelText(installLabel('v1.9.0'));
    expect(screen.queryByText(i18next.t('coreUpdates.refreshFailed'))).toBeNull();
  });

  it('preserves the installed build and release list after a refused download', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', versions));
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(false, 'checksum refused'));
    const onInstalled = vi.fn();
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={onInstalled} />);
    await confirmInstall();
    await screen.findByText('checksum refused');
    expect(screen.getByText(i18next.t('coreUpdates.installedOnDisk', { version: '1.12.0' }))).toBeTruthy();
    expect(screen.getByText('v1.13.0')).toBeTruthy();
    expect(onInstalled).not.toHaveBeenCalled();
  });

  it('keeps a successful install successful when the following version refresh throws', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValueOnce(new Msg(true, '', versions)).mockRejectedValue(new Error('Release service unavailable'));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'installed'));
    const onInstalled = vi.fn();
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={onInstalled} />);
    await confirmInstall();
    await screen.findByText(i18next.t('coreUpdates.refreshAfterInstallFailed'));
    expect(screen.getByText(i18next.t('coreUpdates.installSucceeded', { version: 'v1.13.0' }))).toBeTruthy();
    expect(screen.getByText(i18next.t('coreUpdates.installedOnDisk', { version: 'v1.13.0' }))).toBeTruthy();
    expect(screen.queryByText(i18next.t('coreUpdates.installFailed', { version: 'v1.13.0' }))).toBeNull();
    expect(onInstalled).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('handles a manual refresh exception while retaining known installed state', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValueOnce(new Msg(true, '', versions)).mockRejectedValue(new Error('Offline'));
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={vi.fn()} />);
    await screen.findByLabelText(installLabel('v1.13.0'));
    fireEvent.click(screen.getByRole('button', { name: i18next.t('refresh') }));
    await screen.findByText('Offline');
    expect(screen.getByText(i18next.t('coreUpdates.installedOnDisk', { version: '1.12.0' }))).toBeTruthy();
    expect(screen.getByLabelText(installLabel('v1.13.0'))).toBeTruthy();
  });

  it('allows reinstalling the current listed build but prevents duplicate confirmations and downloads', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', versions));
    let finish!: (value: Msg) => void;
    const post = vi.spyOn(HttpUtil, 'post').mockImplementation(() => new Promise<Msg>((resolve) => { finish = resolve; }));
    renderWithProviders(<CoreVersionsPanel core={core()} onInstalled={vi.fn()} />);
    const action = await screen.findByLabelText(installLabel('v1.12.0'));
    fireEvent.click(action); fireEvent.click(action);
    expect(screen.getAllByRole('dialog')).toHaveLength(1);
    const confirm = within(screen.getByRole('dialog')).getByRole('button', { name: i18next.t('pages.cores.install') });
    fireEvent.click(confirm); fireEvent.click(confirm); fireEvent.click(action);
    expect(post).toHaveBeenCalledTimes(1);
    await act(async () => { finish(new Msg(true, 'installed')); });
    await screen.findByText(i18next.t('coreUpdates.installSucceeded', { version: 'v1.12.0' }));
  });

  it('uses the next managed start for a stopped core and never offers to restart standalone processes', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', versions));
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'installed'));
    const onRestart = vi.fn();
    const external = { ...core(), state: 'idle', ready: false, instances: [], hostRuntime: { state: 'running', processes: [{ pid: 8000, executable: '/standalone/sing-box', state: 'running', binaryReplaced: true }] } };
    renderWithProviders(<CoreVersionsPanel core={external} onInstalled={vi.fn()} onRestart={onRestart} />);
    await confirmInstall();
    await screen.findByText(i18next.t('coreUpdates.nextStart'));
    expect(screen.getByText(i18next.t('coreUpdates.externalUnaffected'))).toBeTruthy();
    expect(screen.queryByRole('button', { name: i18next.t('coreUpdates.restartManaged') })).toBeNull();
    expect(screen.queryByText(i18next.t('coreUpdates.restartRequired'))).toBeNull();
    expect(onRestart).not.toHaveBeenCalled();
  });

  it('shows a replaced managed executable after reload without requiring a local install acknowledgement', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', { ...versions, installed: '1.13.0' }));
    const post = vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, ''));
    const onRestart = vi.fn();
    const managed = { ...core(), hostRuntime: { state: 'running', processes: [{ pid: 4502, executable: '/panel/sing-box', state: 'running', binaryReplaced: true }] } };
    renderWithProviders(<CoreVersionsPanel core={managed} onInstalled={vi.fn()} onRestart={onRestart} />);
    await screen.findByText(i18next.t('coreUpdates.restartRequired'));
    expect(onRestart).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
  });

  it('keeps a same-version reinstall pending through restart confirmation until replacement PIDs are observed', async () => {
    vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', versions));
    vi.spyOn(HttpUtil, 'post').mockResolvedValue(new Msg(true, 'installed'));
    const onRestart = vi.fn(), onInstalled = vi.fn();
    const original = core();
    const { rerender } = render(<CoreVersionsPanel core={original} onInstalled={onInstalled} onRestart={onRestart} />);
    await confirmInstall('v1.12.0');
    await screen.findByText(i18next.t('coreUpdates.restartRequired'));
    fireEvent.click(screen.getByRole('button', { name: i18next.t('coreUpdates.restartManaged') }));
    expect(onRestart).toHaveBeenCalledTimes(1);
    expect(screen.getByText(i18next.t('coreUpdates.restartRequired'))).toBeTruthy();
    rerender(<CoreVersionsPanel core={original} onInstalled={onInstalled} onRestart={onRestart} restarting />);
    expect(screen.getByText(i18next.t('coreUpdates.restartRequired'))).toBeTruthy();
    const replacement = { ...original, instances: original.instances.map((instance) => ({ ...instance, pid: 9000 })) };
    rerender(<CoreVersionsPanel core={replacement} onInstalled={onInstalled} onRestart={onRestart} />);
    expect(screen.queryByText(i18next.t('coreUpdates.restartRequired'))).toBeNull();
  });

  it.each(['ssh', 'wgkernel', 'openvpn'])('keeps %s read-only without inventing independent install actions', (id) => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue(new Msg(true, '', versions));
    const value = corePageFixtures().find((item) => item.id === id)!;
    renderWithProviders(<CoreVersionsPanel core={{ ...value, version: { ...value.version, reported: true, manageable: false } }} onInstalled={vi.fn()} />);
    expect(get).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: i18next.t('pages.cores.install') })).toBeNull();
    expect(screen.queryByRole('radio')).toBeNull();
  });
});
