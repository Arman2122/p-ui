import { useState } from 'react';
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Form, Input, Modal, Select, Space, Table, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  FleetNetworkAssetPageSchema, FleetNetworkAssetRequestSchema, FleetNetworkAssetViewSchema,
  type FleetNetworkAssetView, type FleetNetworkOperation, type FleetNodeNetworkStateView,
} from '@/generated/zod';
import { FleetAssetKindSchema, type FleetAssetUploadForm } from '@/schemas/forms/fleet-network';
import { HttpUtil, RandomUtil } from '@/utils';
import { networkRule } from './FleetNetworkEditor';

async function assetData(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  try {
    let encoded = '';
    for (let offset = 0; offset < bytes.length; offset += 32768) encoded += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
    return btoa(encoded);
  } finally { bytes.fill(0); }
}

function AssetUpload({ state, existing, onDone, onClose }: {
  state: FleetNodeNetworkStateView; existing?: FleetNetworkAssetView; onDone: () => Promise<void>; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [form] = Form.useForm<FleetAssetUploadForm>();
  const [file, setFile] = useState<File | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [assetID] = useState(() => existing?.assetId ?? RandomUtil.randomUUID());
  const save = async ({ kind }: FleetAssetUploadForm) => {
    if (!file || file.size === 0 || file.size > (8 << 20)) { setError(t('pages.nodes.network.assetSizeError')); return; }
    setSaving(true);
    setError('');
    try {
      const request = FleetNetworkAssetRequestSchema.parse({ assetId: assetID, nodeId: state.nodeId, installationId: state.installationId, expectedRevision: existing?.revision ?? 0, kind, data: await assetData(file) });
      const response = await HttpUtil.post(`/panel/api/fleet/network/nodes/${state.nodeId}/assets`, request, { silent: true, headers: { 'Content-Type': 'application/json' } });
      request.data = '';
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      FleetNetworkAssetViewSchema.parse(response.obj);
      setFile(null);
      form.resetFields();
      onClose();
      await onDone();
    } catch { setError(t('pages.nodes.network.assetSaveFailed')); }
    finally { setSaving(false); }
  };
  return <Modal open title={t(existing ? 'pages.nodes.network.newAssetVersion' : 'pages.nodes.network.uploadAsset')} onCancel={() => { if (!saving) onClose(); }} onOk={() => form.submit()} okButtonProps={{ color: 'default', variant: 'solid' }} confirmLoading={saving} cancelButtonProps={{ disabled: saving }} closable={!saving} keyboard={!saving} mask={{ closable: !saving }} destroyOnHidden>
    <Typography.Paragraph>{t('pages.nodes.network.assetUploadHint')}</Typography.Paragraph>
    {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
    <Form form={form} initialValues={{ kind: existing?.kind ?? 'tls-certificate' }} layout="vertical" onFinish={save}>
      <Form.Item name="kind" label={t('pages.nodes.network.assetKind')} rules={[networkRule(FleetAssetKindSchema, t('pages.nodes.network.invalidField'))]}><Select disabled={Boolean(existing)} options={FleetAssetKindSchema.options.map(value => ({ value, label: t(`pages.nodes.network.assetKinds.${value}`) }))} /></Form.Item>
      <Form.Item label={t('pages.nodes.network.assetFile')} htmlFor="fleet-asset-file" required><Input id="fleet-asset-file" type="file" disabled={saving} onChange={event => setFile(event.target.files?.[0] ?? null)} /></Form.Item>
    </Form>
  </Modal>;
}

function SlotBinding({ slot, kind, state, assets, onSave }: {
  slot: string; kind: string; state: FleetNodeNetworkStateView; assets: FleetNetworkAssetView[];
  onSave: (operation: FleetNetworkOperation) => Promise<void>;
}) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const bound = state.overrides.assetBindings?.[slot];
  const save = async (reset = false) => {
    const candidate = assets.find(asset => `${asset.assetId}:${asset.revision}` === selected && asset.kind === kind);
    if (!reset && !candidate) return;
    setSaving(true);
    setError('');
    try {
      await onSave({ section: 'asset-binding', key: slot, operation: reset ? 'reset' : 'replace', ...(candidate && !reset ? { asset: { assetId: candidate.assetId, revision: candidate.revision } } : {}) });
      setSelected(undefined);
    } catch (error) { setError(error instanceof Error ? error.message : t('somethingWentWrong')); }
    finally { setSaving(false); }
  };
  return <Space orientation="vertical" style={{ width: '100%' }}>
    <Typography.Text strong>{slot} · {t(`pages.nodes.network.assetKinds.${kind}`)}</Typography.Text>
    <Typography.Text>{bound ? `${bound.assetId} · v${bound.revision}` : t('pages.nodes.network.unbound')}</Typography.Text>
    <Space wrap style={{ width: '100%' }} styles={{ item: { maxWidth: '100%' } }}>
      <Select aria-label={t('pages.nodes.network.selectAssetFor', { slot })} value={selected} placeholder={t('pages.nodes.network.selectAsset')} style={{ width: 280, maxWidth: '100%' }} onChange={setSelected} options={assets.filter(asset => asset.kind === kind).map(asset => ({ value: `${asset.assetId}:${asset.revision}`, label: `${asset.assetId} · v${asset.revision}` }))} />
      <Button disabled={!selected} loading={saving} onClick={() => { void save(); }}>{t('pages.nodes.network.bindAsset')}</Button>
      {bound && <Button disabled={saving} onClick={() => { void save(true); }}>{t('pages.nodes.network.unbindAsset')}</Button>}
    </Space>
    {error && <Alert type="error" title={error} />}
  </Space>;
}

export default function FleetNetworkAssets({ state, onSave }: { state: FleetNodeNetworkStateView; onSave: (operation: FleetNetworkOperation) => Promise<void> }) {
  const { t } = useTranslation();
  const cache = useQueryClient();
  const [upload, setUpload] = useState<{ existing?: FleetNetworkAssetView; installationId: string }>();
  const assets = useInfiniteQuery({
    queryKey: ['fleet', 'network', 'assets', state.nodeId, state.installationId], initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const response = await HttpUtil.get(`/panel/api/fleet/network/nodes/${state.nodeId}/assets`, { after: pageParam, limit: 100 }, { silent: true });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      return FleetNetworkAssetPageSchema.parse(response.obj);
    },
    getNextPageParam: page => page.nextCursor || undefined,
  });
  const list = assets.data?.pages.flatMap(page => page.items) ?? [];
  return <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
    <Alert type="info" showIcon title={t('pages.nodes.network.assetMetadataHint')} />
    <Button onClick={() => setUpload({ installationId: state.installationId })}>{t('pages.nodes.network.uploadAsset')}</Button>
    {assets.error && <Alert type="error" title={assets.error.message} />}
    <Table rowKey="assetId" size="small" loading={assets.isPending} dataSource={list} pagination={false} scroll={{ x: 600 }} columns={[
      { title: t('pages.nodes.network.assetKind'), render: (_value, asset) => <Space orientation="vertical" size={0}><Typography.Text>{t(`pages.nodes.network.assetKinds.${asset.kind}`)}</Typography.Text><Typography.Text copyable>{asset.assetId}</Typography.Text></Space> },
      { title: t('pages.nodes.network.revision'), dataIndex: 'revision' },
      { title: t('pages.nodes.network.assetSize'), dataIndex: 'size' },
      { title: t('pages.nodes.network.digest'), render: (_value, asset) => <Typography.Text code copyable style={{ overflowWrap: 'anywhere' }}>{asset.sha256}</Typography.Text> },
      { title: t('pages.nodes.network.actions'), render: (_value, asset) => <Button size="small" onClick={() => setUpload({ existing: asset, installationId: state.installationId })}>{t('pages.nodes.network.newAssetVersion')}</Button> },
    ]} />
    {assets.hasNextPage && <Button loading={assets.isFetchingNextPage} onClick={() => { void assets.fetchNextPage(); }}>{t('pages.nodes.network.moreAssets')}</Button>}
    {Object.entries(state.effective.assetSlots ?? {}).map(([slot, kind]) => <SlotBinding key={`${slot}:${state.revision.id}`} slot={slot} kind={kind} state={state} assets={list} onSave={onSave} />)}
    {upload && <AssetUpload state={{ ...state, installationId: upload.installationId }} existing={upload.existing} onClose={() => setUpload(undefined)} onDone={() => cache.invalidateQueries({ queryKey: ['fleet', 'network', 'assets', state.nodeId] })} />}
  </Space>;
}
