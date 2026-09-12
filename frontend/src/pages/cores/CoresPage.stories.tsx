import { useEffect, useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, spyOn, waitFor } from 'storybook/test';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { keys } from '@/api/queryKeys';
import { ThemeProvider, useTheme } from '@/hooks/useTheme';
import { useTokenBridge } from '@/hooks/useTokenBridge';
import { HttpUtil, Msg } from '@/utils';
import CoresPage from './CoresPage';
import { corePageFixtures, coreDescriptorFixtures, CORE_INBOUND_FIXTURES } from './CoresPage.fixtures';

function CoreStorySurface({ dark }: { dark: boolean }) {
  const { isDark, toggleTheme } = useTheme();
  useTokenBridge();
  useEffect(() => { if (isDark !== dark) toggleTheme(); }, [dark, isDark, toggleTheme]);
  return <main className="p-page"><CoresPage /></main>;
}

function FullCoresPage({ dark = false }: { dark?: boolean }) {
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    cache.setQueryData(keys.cores.status(), corePageFixtures());
    cache.setQueryData(keys.cores.list(), coreDescriptorFixtures());
    cache.setQueryData(keys.inbounds.options(), CORE_INBOUND_FIXTURES);
    return cache;
  });
  return <MemoryRouter><QueryClientProvider client={client}><ThemeProvider><CoreStorySurface dark={dark} /></ThemeProvider></QueryClientProvider></MemoryRouter>;
}

const meta = {
  title: 'Pages/Cores', component: FullCoresPage, tags: ['autodocs'], parameters: { layout: 'fullscreen' },
  render: (_, context) => <FullCoresPage dark={context.globals.theme === 'dark'} />,
  play: async ({ canvasElement, globals }) => {
    await waitFor(() => expect(canvasElement.querySelectorAll('[data-core-id]')).toHaveLength(8));
    await waitFor(() => expect(canvasElement.querySelector('.cores-page')?.classList.contains('is-dark')).toBe(globals.theme === 'dark'));
    await waitFor(() => {
      const button = canvasElement.querySelector<HTMLElement>('.p-corerow .p-name')!;
      const expected = getComputedStyle(document.documentElement).getPropertyValue('--p-text').replace(/\s/g, '');
      expect(getComputedStyle(button).color.replace(/\s/g, '')).toBe(expected);
    });
  },
  beforeEach: () => {
    const get = spyOn(HttpUtil, 'get').mockImplementation(async function<T>(url: string) {
      let obj: unknown = null;
      if (url === '/panel/api/cores/status') obj = corePageFixtures();
      else if (url === '/panel/api/cores') obj = coreDescriptorFixtures();
      else if (url === '/panel/api/inbounds/options') obj = CORE_INBOUND_FIXTURES;
      else if (url.endsWith('/logs')) obj = ['Listener configured', 'Control channel is ready'];
      else if (url.endsWith('/versions')) obj = { manageable: true, installed: url.includes('/mtproto/') ? '1.0.1' : '26.7.28', available: url.includes('/mtproto/') ? [] : ['v26.7.28'] };
      return new Msg(true, '', obj as T);
    });
    const post = spyOn(HttpUtil, 'post').mockImplementation(async function<T>() { return new Msg<T>(true, 'Story action recorded'); });
    return () => { get.mockRestore(); post.mockRestore(); };
  },
} satisfies Meta<typeof FullCoresPage>;
export default meta;
type Story = StoryObj<typeof meta>;
export const EightCores: Story = {};
export const EightCoresDark: Story = { globals: { theme: 'dark' } };
