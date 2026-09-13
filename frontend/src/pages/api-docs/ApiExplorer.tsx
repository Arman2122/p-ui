import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Drawer, Empty, Input, Segmented, Select, Skeleton, Space, message } from 'antd';
import type { InputRef } from 'antd';
import { CopyOutlined, DownloadOutlined, KeyOutlined, SearchOutlined, UnorderedListOutlined } from '@ant-design/icons';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { useSignedInUser } from '@/pages/resellers/api';
import type { SignedInUser } from '@/pages/resellers/schema';
import { ClipboardManager } from '@/utils';
import { OpenApiDocumentSchema, buildApiCatalogue, inlineDocText, normalizeApiPath, type ApiEntry, type OpenApiDocument } from './api-document';
import { ApiReference, ApiEventReference, ApiMethod, DocText } from './ApiReference';
import ApiRequestPanel from './ApiRequestPanel';
import type { ApiRequestDraft } from './request-inputs';
import './ApiDocsPage.css';

const basePath = (window.PUI_BASE_PATH || '/').replace(/\/$/, '');
const openApiUrl = `${basePath}/panel/api/openapi.json`;

function shortPath(entry: ApiEntry) { return entry.specPath.replace(/^\/panel\/api(?=\/)/, ''); }
function entrySummary(entry: ApiEntry) { return inlineDocText(entry.summary).split(/(?<=[.!?])\s/)[0]; }
function canonicalKey(value: string | null) { return value ? normalizeApiPath(value) : ''; }

function NavigationContainer({ mobile, open, onClose, search, children }: { mobile: boolean; open: boolean; onClose: () => void; search: ReactNode; children: ReactNode }) {
  const { t } = useTranslation();
  const { isDark, isUltra } = useTheme();
  if (!mobile) return children;
  return <Drawer title={t('apiUx.browseEndpoints')} placement="left" size={360} open={open} onClose={onClose} destroyOnHidden
    rootClassName={`api-docs-page api-ux api-nav-drawer${isDark ? ' is-dark' : ''}${isUltra ? ' is-ultra' : ''}`}>
    {search}{children}
  </Drawer>;
}

function permitted(entry: ApiEntry, user: SignedInUser) {
  if (user.role !== 'reseller') return true;
  const allowed = entry.path === '/panel/api/me' || /^\/panel\/api\/activity(?:\/(?:read|alerts))?$/.test(entry.path) || entry.path.startsWith('/panel/api/reseller/');
  return allowed && entry.available && (user.permissions.plans || !/^\/panel\/api\/reseller\/plans(?:\/|$)/.test(entry.path));
}

export default function ApiExplorer() {
  const { t } = useTranslation();
  const me = useSignedInUser();
  if (me.isError) return <Alert type="error" showIcon title={t('somethingWentWrong')} description={me.error.message}
    action={<Button onClick={() => { void me.refetch(); }}>{t('refresh')}</Button>} />;
  if (!me.data) return <div aria-label={t('loading')}><Skeleton active /><Skeleton active /></div>;
  return <AuthenticatedExplorer key={`${me.data.id}:${me.data.role}:${me.data.permissions.plans}`} user={me.data} />;
}

function AuthenticatedExplorer({ user }: { user: SignedInUser }) {
  const { t } = useTranslation();
  const { isDark, isUltra } = useTheme();
  const { isMobile } = useMediaQuery(760);
  const navigate = useNavigate();
  const [urlParams, setUrlParams] = useSearchParams();
  const [search, setSearch] = useState('');
  const [method, setMethod] = useState('all');
  const [group, setGroup] = useState('all');
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const [authMode, setAuthMode] = useState<'session' | 'bearer'>('session');
  const [token, setToken] = useState('');
  const [draftCache] = useState(() => new Map<string, ApiRequestDraft>());
  const [recent, setRecent] = useState<string[]>([]);
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>({});
  const [navOpen, setNavOpen] = useState(false);
  const searchRef = useRef<InputRef>(null);
  const operationNav = useRef<HTMLElement>(null);
  const [messageApi, messageHolder] = message.useMessage();
  const view = urlParams.get('view') === 'explorer' ? 'explorer' : 'catalogue';
  const chosen = urlParams.get('endpoint');
  const query = useQuery({ queryKey: ['openapi', user.id, user.role, user.permissions.plans], staleTime: 60_000,
    queryFn: async ({ signal }) => {
      const response = await fetch(openApiUrl, { credentials: 'same-origin', signal, headers: { Accept: 'application/json' } });
      if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
      return OpenApiDocumentSchema.parse(await response.json());
    } });
  const all = useMemo(() => query.data ? buildApiCatalogue(query.data).filter(entry => permitted(entry, user)) : [], [query.data, user]);
  const matches = useMemo(() => {
    const terms = search.toLowerCase().trim().split(/\s+/).filter(Boolean);
    const score = (entry: ApiEntry) => terms.reduce((total, term) => total + (entry.specPath.toLowerCase().includes(term) ? 12 : entry.method.toLowerCase() === term ? 10 : entry.group.toLowerCase().includes(term) ? 6 : 1), 0);
    return all.filter(entry => (method === 'all' || entry.method === method) && terms.every(term => `${entry.method} ${entry.path} ${entry.specPath} ${entry.summary} ${entry.group}`.toLowerCase().includes(term)))
      .sort((a, b) => score(b) - score(a));
  }, [all, search, method]);
  const groups = [...new Map(all.map(entry => [entry.groupId, entry.group])).entries()];
  const matchingGroups = groups.filter(([id]) => group === id || matches.some(entry => entry.groupId === id))
    .sort(([a], [b]) => search ? matches.findIndex(entry => entry.groupId === a) - matches.findIndex(entry => entry.groupId === b) : 0);
  const visible = matches.filter(entry => group === 'all' || entry.groupId === group);
  const selected = all.find(entry => entry.key === canonicalKey(chosen)) ?? all.find(entry => entry.path === '/panel/api/me' && entry.method === 'GET') ?? all.find(entry => entry.method === 'GET' && entry.available) ?? all[0];
  const recentEntries = recent.flatMap(key => { const entry = all.find(item => item.key === key); return entry ? [entry] : []; });
  const starters = [
    { path: '/panel/api/me', label: 'apiUx.startIdentity' },
    { path: user.role === 'reseller' ? '/panel/api/reseller/clients' : '/panel/api/clients/list/paged', label: 'apiUx.startClients' },
    { path: '/panel/api/server/status', label: 'apiUx.startStatus' },
  ].flatMap(item => { const entry = all.find(candidate => candidate.method === 'GET' && candidate.path === item.path && candidate.available); return entry ? [{ ...item, entry }] : []; });
  const allExpanded = visible.length > 0 && visible.every(entry => expanded.has(entry.key));
  const baseUrl = `${window.location.origin}${basePath}`;
  const setView = (value: string, entry?: ApiEntry) => {
    if (entry) {
      setRecent(previous => [entry.key, ...previous.filter(key => key !== entry.key)].slice(0, 6));
      setOpenGroups(previous => ({ ...previous, [entry.groupId]: true }));
      setNavOpen(false);
    }
    setUrlParams(previous => {
      const next = new URLSearchParams(previous);
      if (value === 'explorer') next.set('view', value); else next.delete('view');
      if (entry) next.set('endpoint', entry.key);
      return next;
    });
  };
  const copy = async (value: string) => {
    if (await ClipboardManager.copyText(value)) messageApi.success(t('apiExplorer.copied'));
    else messageApi.error(t('apiExplorer.copyFailed'));
  };
  const setExpandedKey = (key: string, open: boolean) => setExpanded(previous => {
    if (previous.has(key) === open) return previous;
    const next = new Set(previous); if (open) next.add(key); else next.delete(key); return next;
  });
  const reset = () => { setSearch(''); setMethod('all'); setGroup('all'); };
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      if (event.key !== '/' || event.altKey || event.ctrlKey || event.metaKey) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest('input,textarea,select,[contenteditable="true"],[role="textbox"],[role="combobox"],[role="dialog"]')) return;
      event.preventDefault(); searchRef.current?.focus();
    };
    window.addEventListener('keydown', focusSearch);
    return () => window.removeEventListener('keydown', focusSearch);
  }, []);
  useEffect(() => {
    const nav = operationNav.current;
    if (!nav) return;
    const fit = () => {
      const height = Math.max(140, window.innerHeight - Math.max(0, nav.getBoundingClientRect().top) - 20);
      const value = `${Math.floor(height)}px`;
      if (nav.style.maxHeight !== value) nav.style.maxHeight = value;
    };
    const reveal = () => {
      fit();
      const active = nav.querySelector<HTMLElement>('.p-eprow[aria-pressed="true"]');
      if (active?.getClientRects().length) nav.scrollTop += active.getBoundingClientRect().top - nav.getBoundingClientRect().top - 36;
    };
    const frame = requestAnimationFrame(reveal);
    let resizeFrame = 0;
    const scheduleFit = () => {
      if (resizeFrame) return;
      resizeFrame = requestAnimationFrame(() => { resizeFrame = 0; fit(); });
    };
    const observer = new ResizeObserver(scheduleFit);
    if (nav.parentElement) observer.observe(nav.parentElement);
    window.addEventListener('resize', scheduleFit);
    return () => { cancelAnimationFrame(frame); cancelAnimationFrame(resizeFrame); observer.disconnect(); window.removeEventListener('resize', scheduleFit); };
  }, [selected?.key, view, navOpen, isMobile]);

  return <div className={`p-network-page api-docs-page api-ux${isDark ? ' is-dark' : ''}${isUltra ? ' is-ultra' : ''}`}>
    {messageHolder}
    <PageHeader title={t('menu.apiDocs')} sub={t('apiExplorer.subtitle')} actions={<Space wrap>
      <Button icon={<KeyOutlined />} onClick={() => navigate(user.role === 'reseller' ? '/reseller/account' : '/settings#tokens')}>{t('design.apiTokens')}</Button>
      <Button icon={<DownloadOutlined />} href={openApiUrl} download="openapi.json">{t('design.apiDownload')}</Button>
    </Space>} />
    <section className="p-card p-apibar" aria-label={t('apiExplorer.connection')}>
      <div className="api-base"><span className="p-kicker">{t('apiExplorer.baseUrl')}</span><div>
        <code dir="ltr">{baseUrl}</code><Button type="text" size="small" icon={<CopyOutlined />} aria-label={t('apiExplorer.copyBase')} onClick={() => { void copy(baseUrl); }} />
      </div></div>
      <div className="api-auth"><label htmlFor="api-auth-mode" className="p-kicker">{t('apiExplorer.sendAs')}</label>
        <Select id="api-auth-mode" aria-label={t('apiExplorer.sendAs')} value={authMode} onChange={value => { setAuthMode(value); if (value === 'session') setToken(''); }}
          options={[{ value: 'session', label: `${t('apiExplorer.session')} · ${user.username}` }, { value: 'bearer', label: t('apiExplorer.bearer') }]} />
      </div>
      {authMode === 'bearer' && <div className="api-auth-token"><label htmlFor="api-bearer" className="p-kicker">{t('apiExplorer.bearer')}</label>
        <Input.Password id="api-bearer" autoComplete="off" value={token} onChange={event => setToken(event.target.value)} placeholder={t('apiExplorer.tokenPlaceholder')} />
        <span className="p-sub">{t('apiExplorer.tokenMemory')}</span>
      </div>}
    </section>
    <div className="api-browse-tools">
      <Segmented aria-label={t('apiExplorer.view')} value={view} onChange={value => setView(value)} options={[{ value: 'catalogue', label: t('apiExplorer.catalogue') }, { value: 'explorer', label: t('apiExplorer.explorer') }]} />
      <Input ref={searchRef} className="api-search" prefix={<SearchOutlined />} suffix={<kbd className="api-search-key" title={t('apiUx.searchShortcut')}>/</kbd>} allowClear value={search} onChange={event => setSearch(event.target.value)} placeholder={t('design.apiSearch')} aria-label={t('design.apiSearch')}
        onKeyDown={event => {
          if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setSearch(''); }
          if (event.key === 'Enter' && search.trim() && view === 'explorer' && visible[0]) { event.preventDefault(); setView('explorer', visible[0]); }
        }} />
      <Select aria-label={t('apiExplorer.method')} value={method} onChange={setMethod} options={[{ value: 'all', label: t('apiExplorer.allMethods') }, ...[...new Set(all.map(entry => entry.method))].map(value => ({ value, label: value }))]} />
      <span className="p-sub" role="status">{t('apiExplorer.resultCount', { shown: visible.length, total: all.length })}</span>
      <Button className="api-nav-toggle" icon={<UnorderedListOutlined />} aria-label={t('apiUx.browseEndpoints')} aria-expanded={navOpen} onClick={() => setNavOpen(value => !value)}>{t('apiUx.browseEndpoints')}</Button>
    </div>
    {!!starters.length && <div className="api-quickstart" aria-label={t('apiUx.quickstart')}><span className="p-sub">{t('apiUx.quickstart')}</span>{starters.map(item => <Button key={item.path} size="small" aria-label={t(item.label)} onClick={() => { reset(); setView('explorer', item.entry); }}><ApiMethod method="GET" />{t(item.label)}</Button>)}</div>}
    {query.isError && <Alert type="error" showIcon title={t('apiExplorer.loadFailed')} description={query.error.message}
      action={<Button onClick={() => { void query.refetch(); }}>{t('refresh')}</Button>} />}
    {query.isPending ? <Skeleton active paragraph={{ rows: 8 }} /> : query.data && <div className="p-api">
      <NavigationContainer mobile={isMobile} open={navOpen} onClose={() => setNavOpen(false)} search={<Input className="api-drawer-search" prefix={<SearchOutlined />} allowClear autoFocus
        aria-label={t('apiUx.searchInNavigation')} placeholder={t('design.apiSearch')} value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => {
          if (event.key === 'Escape' && search) { event.stopPropagation(); setSearch(''); }
          if (event.key === 'Enter' && search.trim() && view === 'explorer' && visible[0]) setView('explorer', visible[0]);
        }} />}>
      <PanelCard className={`p-api-rail${navOpen ? ' is-open' : ''}`} pad={8} title={t(view === 'catalogue' ? 'apiExplorer.categories' : 'design.allEndpoints')}>
        {view === 'catalogue' ? <nav aria-label={t('apiExplorer.categories')}>
          <button className={`api-group${group === 'all' ? ' is-active' : ''}`} onClick={() => setGroup('all')} aria-pressed={group === 'all'}><span>{t('design.allEndpoints')}</span><span>{matches.length}</span></button>
          {matchingGroups.map(([id, name]) => <button key={id} className={`api-group${group === id ? ' is-active' : ''}`} aria-pressed={group === id} onClick={() => setGroup(id)}><span>{name}</span><span>{matches.filter(entry => entry.groupId === id).length}</span></button>)}
        </nav> : <Select className="api-rail-category" aria-label={t('apiExplorer.categories')} value={group} onChange={value => { setGroup(value); setOpenGroups(previous => ({ ...previous, [value]: true })); if (operationNav.current) operationNav.current.scrollTop = 0; }}
          options={[{ value: 'all', label: `${t('design.allEndpoints')} (${matches.length})` }, ...matchingGroups.map(([id, name]) => ({ value: id, label: `${name} (${matches.filter(entry => entry.groupId === id).length})` }))]} />}
        {view === 'explorer' && !search && !!recentEntries.length && <details className="api-recent"><summary>{t('apiUx.recent')} <span>{recentEntries.length}</span></summary><section aria-label={t('apiUx.recent')}>
            {recentEntries.map(entry => <button key={entry.key} className="api-recent-entry" aria-label={`${t('apiUx.openRecent')}: ${entry.method} ${entry.path}`} onClick={() => { reset(); setView('explorer', entry); }}><ApiMethod method={entry.method} /><code dir="ltr">{shortPath(entry)}</code></button>)}
          </section></details>}
        {view === 'explorer' && <nav ref={operationNav} className="api-operation-rail" aria-label={t('apiExplorer.operations')}>
          {matchingGroups.filter(([id]) => visible.some(entry => entry.groupId === id)).map(([id, label]) => <details key={id} className="api-navgroup"
            open={!!search || (openGroups[id] ?? selected?.groupId === id)} onToggle={event => {
              if (search) return;
              const open = event.currentTarget.open;
              setOpenGroups(previous => previous[id] === open ? previous : { ...previous, [id]: open });
            }}>
            <summary>{label}<span>{visible.filter(entry => entry.groupId === id).length}</span></summary>
            {visible.filter(entry => entry.groupId === id).map(entry => <button key={entry.key} className={`p-eprow${selected?.key === entry.key ? ' is-active' : ''}`}
              aria-label={`${entry.method} ${entry.path}`} aria-pressed={selected?.key === entry.key} onClick={() => setView('explorer', entry)}>
              <div className="api-nav-entry-path"><ApiMethod method={entry.method} /><code dir="ltr" title={entry.path}>{shortPath(entry)}</code></div>
              <span className="api-nav-entry-summary" title={inlineDocText(entry.summary)}>{entrySummary(entry)}</span>
            </button>)}
          </details>)}
          {!visible.length && <div className="api-nav-empty"><p>{t('apiExplorer.noMatches')}</p><Button size="small" onClick={reset}>{t('apiExplorer.resetFilters')}</Button></div>}
        </nav>}
      </PanelCard>
      </NavigationContainer>
      <div className="api-document-content">
        {!visible.length && view === 'catalogue' ? <PanelCard><Empty description={t('apiExplorer.noMatches')}><Button onClick={reset}>{t('apiExplorer.resetFilters')}</Button></Empty></PanelCard>
          : view === 'catalogue' ? <>
            <div className="api-reference-toolbar"><p className="p-sub">{t('apiExplorer.catalogueHint')}</p><Button onClick={() => setExpanded(previous => {
              const next = new Set(previous); for (const entry of visible) { if (allExpanded) next.delete(entry.key); else next.add(entry.key); } return next;
            })}>{t(allExpanded ? 'apiExplorer.collapseAll' : 'apiExplorer.expandAll')}</Button></div>
            {groups.filter(([id]) => visible.some(entry => entry.groupId === id)).map(([id, name]) => <section className="api-reference-group" key={id} aria-label={name}>
              <div className="api-group-heading"><h2>{name}</h2><span className="p-sub">{visible.filter(entry => entry.groupId === id).length}</span></div>
              {visible.filter(entry => entry.groupId === id).map(entry => <article className="p-card api-reference-card" key={entry.key} data-api-key={entry.key}>
                <details open={expanded.has(entry.key)} onToggle={event => setExpandedKey(entry.key, event.currentTarget.open)}>
                  <summary><div className="api-reference-summary"><ApiMethod method={entry.method} /><code dir="ltr">{entry.path}</code>
                    {entry.deprecated && <span className="api-deprecated">{t('apiExplorer.deprecated')}</span>}</div><p><DocText text={entry.summary} /></p></summary>
                  {expanded.has(entry.key) && <div className="api-reference-body"><ApiReference entry={entry} document={query.data!} /></div>}
                </details>
                <div className="api-reference-actions"><Button size="small" type="text" icon={<CopyOutlined />} aria-label={t('apiExplorer.copyLink')} title={t('apiExplorer.copyLink')} onClick={() => { void copy(`${window.location.origin}${basePath}/panel/api-docs?view=explorer&endpoint=${encodeURIComponent(entry.key)}`); }} />
                  <Button size="small" onClick={() => setView('explorer', entry)}>{t(entry.kind === 'websocket' || entry.path === '/ws' ? 'apiExplorer.viewEvent' : 'apiExplorer.openExplorer')}</Button></div>
              </article>)}
            </section>)}
          </> : selected ? <>
            {!visible.some(entry => entry.key === selected.key) && <div className="api-selection-note"><span>{t('apiUx.selectionOutsideSearch')}</span><Button type="link" size="small" onClick={reset}>{t('apiExplorer.resetFilters')}</Button></div>}
            <OperationView key={selected.key} entry={selected} document={query.data} basePath={basePath} auth={{ mode: authMode, token, identityKey: `${user.id}:${user.role}` }} draftCache={draftCache} />
          </> : <PanelCard><Empty description={t('noData')} /></PanelCard>}
      </div>
    </div>}
  </div>;
}

function OperationView({ entry, document, basePath: prefix, auth, draftCache }: {
  entry: ApiEntry; document: OpenApiDocument; basePath: string;
  auth: { mode: 'session' | 'bearer'; token: string; identityKey: string };
  draftCache: Map<string, ApiRequestDraft>;
}) {
  const { t } = useTranslation();
  if (entry.kind === 'websocket' || entry.path === '/ws') return <ApiEventReference entry={entry} document={document} />;
  if (entry.groupId === 'subscription-server' || /^\/\{(?:subPath|jsonPath|clashPath)\}/.test(entry.path)) return <>
    <PanelCard title={<><ApiMethod method={entry.method} /><code>{entry.path}</code></>}><DocText text={entry.summary} /><p className="p-sub">{t('apiExplorer.subscriptionServer')}</p></PanelCard>
    <PanelCard title={t('apiExplorer.reference')}><ApiReference entry={entry} document={document} /></PanelCard>
  </>;
  if (!entry.available) return <PanelCard title={<><ApiMethod method={entry.method} /><code>{entry.path}</code></>}><DocText text={entry.summary} /><Alert type="info" title={t('apiExplorer.referenceOnly')} /></PanelCard>;
  return <><ApiRequestPanel endpoint={entry} document={document} basePath={prefix} auth={auth} draftCache={draftCache} />
    <details className="p-card api-schema-reference"><summary>{t('apiExplorer.reference')}</summary><div><ApiReference entry={entry} document={document} /></div></details>
  </>;
}
