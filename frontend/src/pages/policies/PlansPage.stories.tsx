import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { keys } from '@/api/queryKeys';
import { PlanRecordSchema } from '@/schemas/api/policy';
import PlansPage from './PlansPage';
import '@/styles/design-pages.css';

function Catalogue() {
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    cache.setQueryData(keys.policies.list(), [
      { id: 1, name: 'Sina 50', quotaBytes: 50 * 1024 ** 3, durationDays: 30, limitIp: 2, tiers: [{ fromBytes: 0, upBps: 10_000_000, downBps: 20_000_000 }, { fromBytes: 40 * 1024 ** 3, upBps: 2_000_000, downBps: 5_000_000 }], rev: 1, createdAt: 0, updatedAt: 0 },
      { id: 2, name: 'Sina 200', quotaBytes: 200 * 1024 ** 3, durationDays: 90, limitIp: 4, tiers: [], rev: 1, createdAt: 0, updatedAt: 0 },
    ].map((p) => PlanRecordSchema.parse(p)));
    cache.setQueryData(keys.cores.list(), []);
    cache.setQueryData(keys.inbounds.options(), []);
    cache.setQueryData([...keys.policies.root(), 'summary', false], { withoutPlan: 4 });
    for (const id of [1, 2]) cache.setQueryData([...keys.policies.root(), 'members', id, 1, false], { items: [], total: 0 });
    return cache;
  });
  return <MemoryRouter><QueryClientProvider client={client}><main className="p-page"><PlansPage /></main></QueryClientProvider></MemoryRouter>;
}

const meta = { title: 'Pages/Plans', component: Catalogue, parameters: { layout: 'fullscreen' } } satisfies Meta<typeof Catalogue>;
export default meta;
type Story = StoryObj<typeof meta>;
export const CatalogueView: Story = {};
