import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { HttpUtil, Msg } from '@/utils';
import { keys } from '@/api/queryKeys';
import CoreDetailModal from './CoreDetailModal';
import { CORE_INBOUND_FIXTURES, coreDescriptorFixtures, corePageFixtures } from './CoresPage.fixtures';
import './CoresPage.css';

type Outcome = 'success' | 'refresh-failure' | 'refusal' | 'external';
const release = 'v1.14.1';
const installLabel = () => i18next.t('pages.cores.installConfirm', { core: 'sing-box', version: release });

function InstallStory({ outcome = 'success' }: { outcome?: Outcome }) {
  const [core, setCore] = useState(() => {
    const fixture = corePageFixtures().find((item) => item.id === 'singbox')!;
    const value = { ...fixture, version: { ...fixture.version, installed: '1.14.0' } };
    return outcome === 'external' ? { ...value, state: 'idle', ready: false, instances: [], hostRuntime: { state: 'running', processes: [{ pid: 8000, executable: '/standalone/sing-box', state: 'running', binaryReplaced: true }] } } : value;
  });
  const [restartRequests, setRestartRequests] = useState(0);
  const [installed, setInstalled] = useState(0);
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    cache.setQueryData(keys.cores.list(), coreDescriptorFixtures());
    cache.setQueryData(keys.inbounds.options(), CORE_INBOUND_FIXTURES);
    return cache;
  });
  return <MemoryRouter><QueryClientProvider client={client}>
    <output aria-label="Install acknowledgements">{installed}</output><output aria-label="Restart requests">{restartRequests}</output>
    <CoreDetailModal core={core} initialTab="version" onClose={() => undefined} restarting={false} onRestart={() => setRestartRequests((n) => n + 1)}
      onInstalled={() => { setInstalled((n) => n + 1); setCore((value) => ({ ...value, version: { ...value.version, installed: '1.14.1' } })); }} />
  </QueryClientProvider></MemoryRouter>;
}

const meta = {
  title: 'Pages/Core installation', component: InstallStory, parameters: { layout: 'fullscreen' },
  beforeEach: ({ args }) => {
    let installed = '1.14.0', acknowledged = false;
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let value: unknown;
      if (url === '/panel/api/cores/singbox/versions') {
        if (acknowledged && args.outcome === 'refresh-failure') throw new Error('Release service unavailable');
        value = { manageable: true, installed, available: ['v1.14.0', release] };
      } else if (url === '/panel/api/cores') value = coreDescriptorFixtures();
      else if (url === '/panel/api/inbounds/options') value = CORE_INBOUND_FIXTURES;
      else if (url === '/panel/api/cores/singbox/logs') value = ['Managed listener log fixture'];
      else throw new Error(`Unexpected story GET: ${url}`);
      return new Msg(true, '', value as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>(url: string) {
      if (url !== '/panel/api/cores/singbox/install/v1.14.1' && url !== '/panel/api/cores/singbox/install/v1.14.0') throw new Error(`Unexpected story mutation: ${url}`);
      if (args.outcome === 'refusal') return new Msg<T>(false, 'Checksum mismatch; the installed file is unchanged');
      installed = url.endsWith('/v1.14.1') ? '1.14.1' : '1.14.0'; acknowledged = true;
      return new Msg<T>(true, 'Installed on disk');
    });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
  play: async ({ canvasElement, args }) => {
    const body = within(document.body);
    const dialog = await body.findByRole('dialog');
    await waitFor(() => expect(dialog).toBeVisible());
    const action = await within(dialog).findByLabelText(installLabel());
    await waitFor(() => expect(action).toBeEnabled());
    await userEvent.click(action);
    const confirm = await waitFor(() => {
      const element = body.getAllByRole('dialog').find((item) => item.textContent?.includes(installLabel()));
      expect(element).toBeDefined();
      return element!;
    });
    await userEvent.click(within(confirm).getByRole('button', { name: i18next.t('pages.cores.install') }));
    if (args.outcome === 'refusal') {
      await within(dialog).findByText('Checksum mismatch; the installed file is unchanged');
      expect(within(dialog).getByText(i18next.t('coreUpdates.installedOnDisk', { version: '1.14.0' }))).toBeVisible();
      expect(canvasElement.querySelector('[aria-label="Install acknowledgements"]')?.textContent).toBe('0');
    } else {
      await within(dialog).findByText(i18next.t('coreUpdates.installSucceeded', { version: release }));
      expect(canvasElement.querySelector('[aria-label="Install acknowledgements"]')?.textContent).toBe('1');
      expect(canvasElement.querySelector('[aria-label="Restart requests"]')?.textContent).toBe('0');
      if (args.outcome === 'refresh-failure') await within(dialog).findByText(i18next.t('coreUpdates.refreshAfterInstallFailed'));
      if (args.outcome === 'external') {
        expect(within(dialog).getByText(i18next.t('coreUpdates.nextStart'))).toBeVisible();
        expect(within(dialog).queryByRole('button', { name: i18next.t('coreUpdates.restartManaged') })).toBeNull();
      } else {
        await userEvent.click(within(dialog).getByRole('button', { name: i18next.t('coreUpdates.restartManaged') }));
        expect(canvasElement.querySelector('[aria-label="Restart requests"]')?.textContent).toBe('1');
        expect(within(dialog).getByText(i18next.t('coreUpdates.restartRequired'))).toBeVisible();
      }
    }
    await waitFor(() => expect(confirm).not.toBeVisible());
    expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth + 1);
  },
} satisfies Meta<typeof InstallStory>;
export default meta;
type Story = StoryObj<typeof meta>;
export const ManagedInstall: Story = { args: { outcome: 'success' } };
export const RefreshFailureDark: Story = { args: { outcome: 'refresh-failure' }, globals: { theme: 'dark' } };
export const RefusedDownload: Story = { args: { outcome: 'refusal' } };
export const ExternalNextStartMobile: Story = { args: { outcome: 'external' }, globals: { theme: 'dark' }, parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
