import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, ConfigProvider, Descriptions, Drawer, Empty, Popconfirm, Select, Space, Spin, Table, Tabs, Tag, Typography, theme } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  FleetNetworkDefaultsRequestSchema, FleetNetworkDefaultsViewSchema, FleetNetworkRevisionViewSchema,
  FleetNodeNetworkRequestSchema, FleetNodeNetworkStateViewSchema, FleetNodeNetworkViewSchema,
  type FleetNetworkOperation, type FleetNetworkSpec, type FleetNetworkPublicationView,
} from '@/generated/zod';
import { defaultsOperation, networkEdit, NetworkSectionSchema, type NetworkEdit, type NetworkSection } from '@/schemas/forms/fleet-network';
import { HttpUtil } from '@/utils';
import FleetNetworkEditor from './FleetNetworkEditor';
import FleetNetworkAssets from './FleetNetworkAssets';
import FleetNetworkPreviewPanel from './FleetNetworkPreview';

const jsonOptions = { silent: true, headers: { 'Content-Type': 'application/json' } };

export default function FleetNetwork({ node, onClose }: { node?: { id: string; name: string }; onClose: () => void }) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const cache = useQueryClient();
  const [section, setSection] = useState<NetworkSection>('inbound');
  const [editing, setEditing] = useState<{ initial: NetworkEdit; revision: number; spec: FleetNetworkSpec; installationId?: string }>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [previewVersion, setPreviewVersion] = useState(0);
  const [published, setPublished] = useState<FleetNetworkPublicationView>();
  const [previewBusy, setPreviewBusy] = useState(false);
  const [activeTab, setActiveTab] = useState('network');
  const defaults = useQuery({
    queryKey: ['fleet', 'network', 'defaults'],
    queryFn: async () => {
      const response = await HttpUtil.get('/panel/api/fleet/network/defaults', undefined, { silent: true });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      return FleetNetworkDefaultsViewSchema.parse(response.obj);
    },
  });
  const current = useQuery({
    queryKey: ['fleet', 'network', 'node', node?.id], enabled: Boolean(node),
    queryFn: async () => {
      const response = await HttpUtil.get(`/panel/api/fleet/network/nodes/${node?.id}`, undefined, { silent: true });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      return FleetNodeNetworkStateViewSchema.parse(response.obj);
    },
  });
  const state = current.data;
  const spec = node ? state?.effective : defaults.data?.specification;
  const revision = node ? state?.revision : defaults.data?.revision;
  const refresh = async () => { await cache.invalidateQueries({ queryKey: ['fleet', 'network'] }); };
  const save = async (operation: FleetNetworkOperation, expected = revision?.revision, snapshot = spec, installationId = state?.installationId) => {
    setPreviewVersion(value => value + 1);
    setPublished(undefined);
    if (expected == null || !snapshot) throw new Error(t('pages.nodes.network.reloadRequired'));
    if (node) {
      if (!state) throw new Error(t('pages.nodes.network.reloadRequired'));
      const request = FleetNodeNetworkRequestSchema.parse({ nodeId: node.id, installationId, expectedRevision: expected, operations: [operation] });
      const response = await HttpUtil.post(`/panel/api/fleet/network/nodes/${node.id}`, request, jsonOptions);
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      FleetNodeNetworkViewSchema.parse(response.obj);
    } else {
      const request = FleetNetworkDefaultsRequestSchema.parse({ expectedRevision: expected, specification: defaultsOperation(snapshot, operation) });
      const response = await HttpUtil.post('/panel/api/fleet/network/defaults', request, jsonOptions);
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      FleetNetworkRevisionViewSchema.parse(response.obj);
    }
    await refresh();
  };
  const changeFollowing = async (followDefaults: boolean) => {
    if (!node || !state || !state.defaults.id) return;
    setSaving(true); setError(''); setPreviewVersion(value => value + 1); setPublished(undefined);
    try {
      const request = FleetNodeNetworkRequestSchema.parse({ nodeId: node.id, installationId: state.installationId, expectedRevision: state.revision.revision, followDefaults, pinnedDefaultsId: followDefaults ? undefined : state.defaults.id, operations: [] });
      const response = await HttpUtil.post(`/panel/api/fleet/network/nodes/${node.id}`, request, jsonOptions);
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      FleetNodeNetworkViewSchema.parse(response.obj);
      await refresh();
    } catch (error) { setError(error instanceof Error ? error.message : t('somethingWentWrong')); }
    finally { setSaving(false); }
  };
  const edit = (selected: NetworkSection, key = '') => {
    setPreviewVersion(value => value + 1); setPublished(undefined);
    if (spec && revision) setEditing({ initial: networkEdit(selected, key, spec), revision: revision.revision, spec, installationId: state?.installationId });
  };
  const reset = async (selected: NetworkSection, key: string, remove = false) => {
    setSaving(true); setError('');
    try { await save({ section: selected, operation: remove ? 'remove' : 'reset', ...(selected === 'inbound' ? { inboundId: Number(key) } : key ? { key } : {}) }); }
    catch (error) { setError(error instanceof Error ? error.message : t('somethingWentWrong')); }
    finally { setSaving(false); }
  };
  const origins = state?.origins ?? {};
  const rows: { id: string; section: NetworkSection; key: string; summary: string; present: boolean }[] = spec ? [
    { id: 'native-network', section: 'native-network', key: '', summary: spec.nativeNetwork ? `${spec.nativeNetwork.peerIPv4} / ${spec.nativeNetwork.gatewayIPv4}` : t('pages.nodes.network.builtin'), present: Boolean(spec.nativeNetwork) },
    { id: 'ssh-host-keys', section: 'ssh-host-keys', key: '', summary: !spec.sshHostKeys || spec.sshHostKeys.generateLocally ? t('pages.nodes.network.generateHostKeys') : spec.sshHostKeys.assetSlot ?? '', present: Boolean(spec.sshHostKeys) },
    ...Object.entries(spec.inbounds ?? {}).map(([key, value]) => ({ id: `inbound/${key}`, section: 'inbound' as const, key, summary: `${value.listen == null ? t('pages.nodes.network.sourceValue') : value.listen || '*'} : ${value.port ?? t('pages.nodes.network.sourceValue')}`, present: true })),
    ...Object.entries(spec.assetSlots ?? {}).map(([key, value]) => ({ id: `asset-slot/${key}`, section: 'asset-slot' as const, key, summary: t(`pages.nodes.network.assetKinds.${value}`), present: true })),
    ...Object.entries(spec.bases ?? {}).map(([key, value]) => ({ id: `base/${key}`, section: 'base' as const, key, summary: value.core, present: true })),
    ...Object.entries(spec.bridges ?? {}).map(([key, value]) => ({ id: `bridge/${key}`, section: 'bridge' as const, key, summary: `${value.runtimeId} → ${value.endpoint}`, present: true })),
    ...Object.entries(origins).filter(([, origin]) => origin === 'removed').map(([id]) => ({ id, section: id.startsWith('base/') ? 'base' as const : id.startsWith('bridge/') ? 'bridge' as const : 'asset-slot' as const, key: id.slice(id.indexOf('/') + 1), summary: t('pages.nodes.network.removed'), present: false })),
  ] : [];
  const content = <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
    {node && state && <>
      <Alert type={state.pending ? 'warning' : 'info'} showIcon title={t(state.pending ? 'pages.nodes.network.pending' : 'pages.nodes.network.savedState')} description={t('pages.nodes.network.savedHint')} />
      <Descriptions size="small" column={1} items={[
        { key: 'defaults', label: t('pages.nodes.network.defaultsRevision'), children: <Space wrap><Tag>{state.defaults.revision}</Tag><Typography.Text>{state.defaults.id || '—'}</Typography.Text></Space> },
        { key: 'follow', label: t('pages.nodes.network.defaultsPolicy'), children: t(state.followDefaults ? 'pages.nodes.network.following' : 'pages.nodes.network.pinned') },
      ]} />
      <Space wrap>
        <Button disabled={saving || !state.defaults.id || state.followDefaults && state.configured} loading={saving} onClick={() => { void changeFollowing(true); }}>{t('pages.nodes.network.followDefaults')}</Button>
        <Button disabled={saving || !state.defaults.id || !state.followDefaults} onClick={() => { void changeFollowing(false); }}>{t('pages.nodes.network.pinDefaults')}</Button>
      </Space>
      {!state.defaults.id && <Alert type="info" title={t('pages.nodes.network.createDefaultsFirst')} />}
    </>}
    {!node && <Alert type="info" showIcon title={t('pages.nodes.network.defaultsHint')} />}
    <Space wrap>
      <Select aria-label={t('pages.nodes.network.section')} value={section} style={{ minWidth: 220 }} onChange={setSection} options={NetworkSectionSchema.options.map(value => ({ value, label: t(`pages.nodes.network.sections.${value}`) }))} />
      <Button disabled={!spec || Boolean(node && !state?.defaults.id)} onClick={() => edit(section)}>{t('pages.nodes.network.addOverride')}</Button>
    </Space>
    <Table rowKey="id" size="small" dataSource={rows} pagination={{ pageSize: 15, hideOnSinglePage: true }} scroll={{ x: 640 }} columns={[
      { title: t('pages.nodes.network.section'), render: (_value, row) => <Space orientation="vertical" size={0}><Typography.Text>{t(`pages.nodes.network.sections.${row.section}`)}</Typography.Text>{row.key && <Typography.Text>{row.key}</Typography.Text>}</Space> },
      { title: t('pages.nodes.network.effectiveValue'), dataIndex: 'summary' },
      ...(node ? [{ title: t('pages.nodes.network.origin'), render: (_value: unknown, row: typeof rows[number]) => <Tag>{t(`pages.nodes.network.origins.${origins[row.id] ?? 'inherited'}`)}</Tag> }] : []),
      { title: t('pages.nodes.network.actions'), render: (_value, row) => <Space wrap>
        <Button size="small" disabled={saving || Boolean(node && !state?.defaults.id)} onClick={() => edit(row.section, row.key)}>{t('pages.nodes.network.edit')}</Button>
        {(node ? origins[row.id] === 'overridden' || origins[row.id] === 'removed' : row.present) && <Button size="small" disabled={saving} onClick={() => { void reset(row.section, row.key); }}>{t(node ? 'pages.nodes.network.resetInherited' : 'pages.nodes.network.resetBuiltin')}</Button>}
        {node && row.present && ['base', 'bridge', 'asset-slot'].includes(row.section) && <Popconfirm title={t('pages.nodes.network.removeConfirm')} okButtonProps={{ color: 'default', variant: 'solid' }} onConfirm={() => reset(row.section, row.key, true)}><Button size="small" danger style={{ color: token.colorText }} disabled={saving}>{t('pages.nodes.network.remove')}</Button></Popconfirm>}
      </Space> },
    ]} />
  </Space>;
  return <ConfigProvider theme={{ components: { Tabs: { itemSelectedColor: token.colorText, itemActiveColor: token.colorText, inkBarColor: token.colorText }, Descriptions: { labelColor: token.colorText } } }}><Drawer open title={node ? t('pages.nodes.network.nodeTitle', { name: node.name }) : t('pages.nodes.network.defaultsTitle')} onClose={() => { if (!saving && !previewBusy) onClose(); }} closable={!saving && !previewBusy} keyboard={!previewBusy} size={920} destroyOnHidden extra={<Button onClick={() => { setPreviewVersion(value => value + 1); void refresh(); }} disabled={saving || previewBusy || Boolean(editing)}>{t('pages.nodes.network.reload')}</Button>}>
    {(defaults.error || current.error || error) && <Alert type="error" showIcon title={error || current.error?.message || defaults.error?.message} style={{ marginBottom: 16 }} />}
    {revision && <Typography.Paragraph>{t('pages.nodes.network.revision')} {revision.revision} · {revision.id || t('pages.nodes.network.notConfigured')}</Typography.Paragraph>}
    {state && <Typography.Paragraph>{t('pages.nodes.network.installation')} <Typography.Text copyable>{state.installationId}</Typography.Text></Typography.Paragraph>}
    {published && <Alert type="success" showIcon title={t('pages.nodes.network.published', { revision: published.deploymentRevision })} style={{ marginBottom: 16 }} />}
    {(!node && defaults.isPending || node && current.isPending) ? <Spin /> : !spec ? <Empty /> : node && state ? <Tabs activeKey={activeTab} tabBarGutter={16} onChange={setActiveTab} items={[
      { key: 'network', disabled: previewBusy && activeTab !== 'network', label: t('pages.nodes.network.networkTab'), children: content },
      { key: 'assets', disabled: previewBusy && activeTab !== 'assets', label: t('pages.nodes.network.assetsTab'), children: <FleetNetworkAssets state={state} onSave={save} /> },
      { key: 'preview', label: t('pages.nodes.network.previewTab'), children: <FleetNetworkPreviewPanel key={`${state.installationId}:${state.revision.id}:${state.defaults.id}:${defaults.data?.revision.id}:${previewVersion}`} state={state} onBusyChange={setPreviewBusy} onPublished={async result => { setPublished(result); await cache.invalidateQueries({ queryKey: ['fleet', 'nodes'] }); await refresh(); }} /> },
    ]} /> : content}
    {editing && <FleetNetworkEditor initial={editing.initial} specification={editing.spec} onClose={() => setEditing(undefined)} onSave={operation => save(operation, editing.revision, editing.spec, editing.installationId)} />}
  </Drawer></ConfigProvider>;
}
