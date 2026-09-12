import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Input, Space, Tag } from 'antd';
import { z } from 'zod';
import SwaggerUI from 'swagger-ui-react';
import 'swagger-ui-react/swagger-ui.css';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { useTheme } from '@/hooks/useTheme';
import { sections } from './endpoints';
import './ApiDocsPage.css';

const SpecSchema = z.record(z.string(), z.unknown());
const PathsSchema = z.record(z.string(), z.record(z.string(), z.unknown()));
const openApiUrl = `${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/panel/api/openapi.json`;

export default function ApiExplorer() {
  const { t } = useTranslation();
  const { isDark, isUltra } = useTheme();
  const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const [chosen, setChosen] = useState('');
  const query = useQuery({ queryKey: ['openapi'], staleTime: 60_000, queryFn: async () => {
    const response = await fetch(openApiUrl, { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
    return SpecSchema.parse(await response.json());
  } });
  const all = useMemo(() => sections.flatMap((s) => s.endpoints.map((e) => ({ ...e, group: s.title, key: `${e.method} ${e.path}` }))), []);
  const visible = all.filter((e) => `${e.method} ${e.path} ${e.summary} ${e.group}`.toLowerCase().includes(search.toLowerCase()));
  const selected = visible.find((e) => e.key === chosen) ?? visible[0];
  const spec = useMemo(() => {
    if (!query.data || !selected) return undefined;
    const paths = PathsSchema.parse(query.data.paths);
    const path = selected.path.replace(/:([a-zA-Z0-9_]+)/g, '{$1}');
    const operation = paths[path];
    if (!operation) return undefined;
    const method = selected.method.toLowerCase();
    return { ...query.data, paths: { [path]: { ...(operation.parameters ? { parameters: operation.parameters } : {}), [method]: operation[method] } } };
  }, [query.data, selected]);

  return <div className={`p-network-page api-docs-page${isDark ? ' is-dark' : ''}${isUltra ? ' is-ultra' : ''}`}>
    <PageHeader title={t('menu.apiDocs')} sub={t('design.apiSubtitle')} actions={<Space wrap>
      <Button onClick={() => navigate('/settings#tokens')}>{t('design.apiTokens')}</Button>
      <Button href={openApiUrl} download="openapi.json">{t('design.apiDownload')}</Button>
    </Space>} />
    {query.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} description={query.error.message}
      action={<Button onClick={() => { void query.refetch(); }}>{t('refresh')}</Button>} />}
    <div className="p-api">
      <PanelCard className="p-api-rail" pad={0} title={t('design.allEndpoints')}>
        <div style={{ padding: 12 }}><Input.Search value={search} onChange={(e) => setSearch(e.target.value)} allowClear
          placeholder={t('design.apiSearch')} aria-label={t('design.apiSearch')} /></div>
        <div className="p-endpoint-list">
          {visible.map((e) => <button key={e.key} className={`p-eprow${selected?.key === e.key ? ' is-active' : ''}`}
            aria-pressed={selected?.key === e.key} onClick={() => setChosen(e.key)}>
            <Tag color={e.method === 'GET' ? 'blue' : 'green'}>{e.method}</Tag><span className="p-mono">{e.path}</span>
          </button>)}
          {!visible.length && <div className="p-plan-empty">{t('noData')}</div>}
        </div>
      </PanelCard>
      <PanelCard pad={14}>
        {selected && <><h2 className="p-h1">{selected.path}</h2><p className="p-sub">{selected.summary}</p></>}
        {query.isPending ? <p>{t('loading')}</p> : spec ? <div className="docs-wrapper">
          <SwaggerUI key={selected?.key} spec={spec} docExpansion="full" deepLinking={false} tryItOutEnabled
            requestInterceptor={(request) => {
              request.credentials = 'same-origin';
              const token = document.querySelector<HTMLMetaElement>('meta[name="csrf-token"]')?.content;
              if (token && typeof request.url === 'string' && new URL(request.url, window.location.href).origin === window.location.origin) {
                request.headers = { ...request.headers, 'X-CSRF-Token': token };
              }
              return request;
            }} />
        </div> : selected && <p className="p-sub">{selected.description || selected.summary}</p>}
      </PanelCard>
    </div>
  </div>;
}
