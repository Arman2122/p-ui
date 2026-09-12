import { useState } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { PageHeader } from '@/components/ui/panel';
import { keys } from '@/api/queryKeys';
import { useRoutingDraft } from '@/hooks/useRoutingDraft';
import { XraySettingsValueSchema, type XraySettingsValue } from '@/schemas/xray';
import RoutingTab from './RoutingTab';

function RoutingPage() {
  const { t } = useTranslation();
  const routing = useRoutingDraft();
  const [template, setTemplate] = useState<XraySettingsValue | null>(() => XraySettingsValueSchema.parse({
    outbounds: [{ protocol: 'freedom', tag: 'direct' }, { protocol: 'blackhole', tag: 'blocked' }],
    routing: { domainStrategy: 'AsIs', rules: [
      { type: 'field', enabled: true, inboundTag: ['api'], outboundTag: 'api' },
      { type: 'field', enabled: true, network: 'tcp,udp', outboundTag: 'direct' },
    ] },
  }));
  return <main className="p-page"><RoutingTab standalone templateSettings={template} setTemplateSettings={setTemplate}
    routing={routing} inboundTags={[]} clientReverseTags={[]} isMobile={false}
    renderHeader={(actions) => <PageHeader title={t('menu.routing')} sub={t('routingPage.firstMatch')} actions={actions} />} /></main>;
}

function StandaloneRouting() {
  const [client] = useState(() => {
    const cache = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, retry: false } } });
    cache.setQueryData(keys.routing.rules(), [
      { id: 1, sortIndex: 0, enable: true, remark: 'Advertising', ingressScope: 'all', ingressIds: '[]', destKind: 'block', destTag: '', destExitId: null, criteria: '{"domain":["geosite:category-ads-all"]}', inspect: false },
      { id: 2, sortIndex: 1, enable: true, remark: 'Private networks', ingressScope: 'all', ingressIds: '[]', destKind: 'block', destTag: '', destExitId: null, criteria: '{"ip":["geoip:private"]}', inspect: false },
      { id: 3, sortIndex: 2, enable: false, remark: 'Direct domains', ingressScope: 'all', ingressIds: '[]', destKind: 'direct', destTag: '', destExitId: null, criteria: '{"domain":["domain:example.org"]}', inspect: false },
    ]);
    cache.setQueryData(keys.routing.subjects(), []);
    cache.setQueryData(keys.routing.exits(), []);
    cache.setQueryData(keys.inbounds.options(), []);
    return cache;
  });
  return <QueryClientProvider client={client}><RoutingPage /></QueryClientProvider>;
}

const meta = {
  title: 'Pages/Routing', component: StandaloneRouting,
  parameters: { layout: 'fullscreen', docs: { description: { component: 'Standalone rules overview with real RoutingDraft edits. The parent Xray page owns save and persistence; this story seeds only read responses.' } } },
} satisfies Meta<typeof StandaloneRouting>;
export default meta;
type Story = StoryObj<typeof meta>;
export const RulesOverview: Story = {};
