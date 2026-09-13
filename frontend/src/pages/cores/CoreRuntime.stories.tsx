import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, userEvent, waitFor, within } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import i18next from 'i18next';
import { keys } from '@/api/queryKeys';
import { HttpUtil, Msg } from '@/utils';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import CoreStatusPopover from '@/layouts/CoreStatusPopover';
import DashboardCores, { CoreSummaryStrip } from '@/pages/index/DashboardCores';
import { PanelCard } from '@/components/ui/panel';
import CoresPage from './CoresPage';
import { coreHostRuntimeFixtures, coreDescriptorFixtures, CORE_INBOUND_FIXTURES, CORE_TRAFFIC_FIXTURES, SSH_BUILD_FIXTURE } from './CoresPage.fixtures';
import '@/pages/index/IndexPage.css';

function RuntimeSurface({ dark }: { dark: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <main className="p-page"><CoreStatusPopover onManage={() => undefined} />
    <div className="index-page ov-page"><PanelCard pad={0}><CoreSummaryStrip cores={coreHostRuntimeFixtures()} onOpen={() => undefined} /></PanelCard>
      <PanelCard><DashboardCores cores={coreHostRuntimeFixtures()} onOpen={() => undefined} /></PanelCard></div><CoresPage /></main>;
}

function CoreRuntimeStory({ dark = false }: { dark?: boolean }) {
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    cache.setQueryData(keys.cores.status(), coreHostRuntimeFixtures());
    cache.setQueryData(keys.cores.list(), coreDescriptorFixtures());
    cache.setQueryData(keys.inbounds.slim(), CORE_TRAFFIC_FIXTURES);
    return cache;
  });
  useEffect(() => () => client.clear(), [client]);
  return <MemoryRouter><QueryClientProvider client={client}><ThemeProvider><RuntimeSurface dark={dark} /></ThemeProvider></QueryClientProvider></MemoryRouter>;
}

const meta = {
  title: 'Pages/Core runtime', component: CoreRuntimeStory, parameters: { layout: 'fullscreen' },
  render: (_, context) => <CoreRuntimeStory dark={context.globals.theme === 'dark'} />,
  beforeEach: () => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let value: unknown;
      if (url === '/panel/api/cores/status') value = coreHostRuntimeFixtures();
      else if (url === '/panel/api/cores') value = coreDescriptorFixtures();
      else if (url === '/panel/api/inbounds/list/slim') value = CORE_TRAFFIC_FIXTURES;
      else if (url === '/panel/api/inbounds/options') value = CORE_INBOUND_FIXTURES;
      else throw new Error(`Unexpected story GET: ${url}`);
      return new Msg(true, '', value as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async () => { throw new Error('Unexpected story mutation'); });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await waitFor(() => expect(canvasElement.querySelectorAll('[data-core-id]')).toHaveLength(8));
    expect(canvasElement.querySelector('.ov-core-summary-count')?.textContent).toBe(i18next.t('pages.index.coresRunning', { running: 7, total: 8 }));
    const page = canvasElement.querySelector<HTMLElement>('.cores-page')!;
    expect(page.scrollWidth).toBeLessThanOrEqual(page.clientWidth + 1);
    const sshRow = page.querySelector<HTMLElement>('[data-core-id="ssh"]')!;
    expect(sshRow.scrollWidth).toBeLessThanOrEqual(sshRow.clientWidth + 1);
    const sshTile = Array.from(canvasElement.querySelectorAll<HTMLButtonElement>('.p-coretile')).find((tile) => tile.textContent?.includes(SSH_BUILD_FIXTURE))!;
    expect(sshTile.scrollWidth).toBeLessThanOrEqual(sshTile.clientWidth + 1);
    expect(sshTile.querySelector('.p-mono')?.getAttribute('title')).toContain(SSH_BUILD_FIXTURE);
    const external = page.querySelector<HTMLElement>('[data-core-id="mtproto"]')!;
    expect(within(external).getByRole('button', { name: i18next.t('pages.cores.restart') })).toBeDisabled();
    expect(within(external).getByRole('button', { name: i18next.t('pages.cores.logs') })).toBeDisabled();
    await userEvent.click(canvas.getByRole('button', { name: i18next.t('menu.cores') }));
    const popover = await within(document.body).findByText(i18next.t('coreRuntime.healthSummary', { healthy: 4, unverified: 3 }));
    const pop = popover.closest('.p-pop')! as HTMLElement;
    expect(pop.scrollWidth).toBeLessThanOrEqual(pop.clientWidth + 1);
    expect(within(pop).getByText(SSH_BUILD_FIXTURE).getAttribute('title')).toContain(SSH_BUILD_FIXTURE);
    await userEvent.click(canvas.getByRole('button', { name: i18next.t('menu.cores') }));
    await userEvent.click(within(sshRow).getByRole('button', { name: 'SSH' }));
    const dialog = await within(document.body).findByRole('dialog');
    await userEvent.click(within(dialog).getByRole('tab', { name: i18next.t('pages.cores.columns.version') }));
    await waitFor(() => expect(within(dialog).getByText(SSH_BUILD_FIXTURE)).toBeVisible());
    expect(within(dialog).getByText(i18next.t('pages.cores.builtInMaintenance'))).toBeVisible();
    expect(within(dialog).queryByRole('radio')).toBeNull();
    expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth + 1);
  },
} satisfies Meta<typeof CoreRuntimeStory>;
export default meta;
type Story = StoryObj<typeof meta>;
export const HostProcesses: Story = {};
export const HostProcessesDark: Story = { globals: { theme: 'dark' } };
export const HostProcessesMobile: Story = { globals: { theme: 'dark' }, parameters: { viewport: { defaultViewport: 'phone', viewports: { phone: { name: 'Phone', styles: { width: '390px', height: '844px' }, type: 'mobile' } } } } };
