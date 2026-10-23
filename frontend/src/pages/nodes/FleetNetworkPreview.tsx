import { useRef, useState } from 'react';
import { Alert, Button, Descriptions, Space, Table, Tag, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import {
  FleetNetworkPreviewRequestSchema, FleetNetworkPreviewSchema, FleetNetworkPublicationViewSchema, FleetNetworkPublishRequestSchema,
  type FleetNetworkPreview, type FleetNetworkPublicationView, type FleetNodeNetworkStateView,
} from '@/generated/zod';
import { HttpUtil, RandomUtil } from '@/utils';
import { hasAuthenticationScope } from '@/schemas/forms/fleet-network';

export default function FleetNetworkPreviewPanel({ state, onPublished, onBusyChange }: {
  state: FleetNodeNetworkStateView; onPublished: (result: FleetNetworkPublicationView) => Promise<void>; onBusyChange?: (busy: boolean) => void;
}) {
  const { t } = useTranslation();
  const [preview, setPreview] = useState<FleetNetworkPreview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const operationID = useRef<string | undefined>(undefined);
  const inFlight = useRef(false);
  const pending = preview?.identities.filter(identity => identity.state !== 'active') ?? [];
  const publishable = preview?.ready === true && pending.every(identity => ['dormant', 'authority_required'].includes(identity.state) && hasAuthenticationScope(identity));
  const authenticationOnly = publishable && pending.length > 0;
  const review = async () => {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true); onBusyChange?.(true); setError(''); setPreview(undefined); operationID.current = undefined;
    try {
      const request = FleetNetworkPreviewRequestSchema.parse({ nodeId: state.nodeId, installationId: state.installationId, expectedRevision: state.revision.revision, expectedDefaultsId: state.defaults.id });
      const response = await HttpUtil.post('/panel/api/fleet/network/preview', request, { silent: true, headers: { 'Content-Type': 'application/json' } });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      const result = FleetNetworkPreviewSchema.parse(response.obj);
      if (result.nodeId !== state.nodeId || result.installationId !== state.installationId || result.nodeRevision.id !== state.revision.id || result.defaults.id !== state.defaults.id) throw new Error(t('pages.nodes.network.reloadRequired'));
      setPreview(result);
    } catch (error) { setError(error instanceof Error ? error.message : t('somethingWentWrong')); }
    finally { inFlight.current = false; setBusy(false); onBusyChange?.(false); }
  };
  const publish = async () => {
    if (!preview || !publishable || inFlight.current) return;
    inFlight.current = true; setBusy(true); onBusyChange?.(true); setError('');
    operationID.current ??= RandomUtil.randomUUID();
    try {
      const request = FleetNetworkPublishRequestSchema.parse({ compilationId: preview.id, operationId: operationID.current });
      const response = await HttpUtil.post('/panel/api/fleet/network/publish', request, { silent: true, headers: { 'Content-Type': 'application/json' } });
      if (!response?.success) throw new Error(response?.msg || t('somethingWentWrong'));
      const result = FleetNetworkPublicationViewSchema.parse(response.obj);
      if (result.nodeId !== state.nodeId || result.compilationId !== preview.id) throw new Error(t('pages.nodes.network.reloadRequired'));
      await onPublished(result);
      setPreview(undefined);
    } catch (error) { setError(error instanceof Error ? error.message : t('somethingWentWrong')); }
    finally { inFlight.current = false; setBusy(false); onBusyChange?.(false); }
  };
  return <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
    <Alert type="info" showIcon title={t('pages.nodes.network.previewHint')} />
    <Space wrap>
      <Button aria-label={t('pages.nodes.network.createPreview')} loading={busy && !preview} disabled={busy || !state.configured} onClick={() => { void review(); }}>{t('pages.nodes.network.createPreview')}</Button>
      <Button aria-label={t('pages.nodes.network.publish')} color="default" variant="solid" loading={busy && Boolean(preview)} disabled={busy || !publishable} onClick={() => { void publish(); }}>{t('pages.nodes.network.publish')}</Button>
    </Space>
    {error && <Alert type="error" showIcon title={error} />}
    {preview && <>
      <Alert type={authenticationOnly ? 'info' : publishable ? 'success' : 'warning'} showIcon title={t(authenticationOnly ? 'pages.nodes.network.authenticationOnlyReady' : publishable ? 'pages.nodes.network.previewReady' : pending.length > 0 ? 'pages.nodes.network.authorityPending' : 'pages.nodes.network.previewBlocked')} description={pending.length > 0 ? t(authenticationOnly ? 'pages.nodes.network.authenticationOnlyHint' : 'pages.nodes.network.authorityPendingHint') : undefined} />
      {pending.length > 0 && <Table rowKey={item => `${item.runtimeId}:${item.clientId}:${item.placementId}`} size="small" pagination={{ pageSize: 10 }} dataSource={pending} columns={[
        { title: t('pages.nodes.network.assignedIdentities'), dataIndex: 'clientId' }, { title: t('pages.nodes.network.entryName'), dataIndex: 'runtimeId' },
        { title: t('pages.nodes.network.authorizationState'), render: (_value, item) => t(`pages.nodes.network.identityStates.${['authority_required', 'dormant', 'cutover_required', 'inactive', 'reconciliation_required'].includes(item.state) ? item.state : 'unknown'}`) },
      ]} />}
      <Descriptions column={1} size="small" items={[
        { key: 'node', label: t('pages.nodes.network.revision'), children: `${preview.nodeRevision.revision} · ${preview.nodeRevision.id}` },
        { key: 'defaults', label: t('pages.nodes.network.defaultsRevision'), children: `${preview.defaults.revision} · ${preview.defaults.id}` },
        { key: 'digest', label: t('pages.nodes.network.digest'), children: <Typography.Text code copyable style={{ overflowWrap: 'anywhere' }}>{preview.sha256}</Typography.Text> },
        { key: 'clients', label: t('pages.nodes.network.assignedIdentities'), children: preview.identities.length },
      ]} />
      {preview.diagnostics.length > 0 && <Table rowKey={item => [item.code, item.runtimeId, item.inboundId, item.assetSlot, item.clientId, item.placementId].join('|')} size="small" pagination={{ pageSize: 10 }} dataSource={preview.diagnostics} columns={[
        { title: t('pages.nodes.network.diagnostic'), render: (_value, item) => <Tag color="warning">{item.code}</Tag> },
        { title: t('pages.nodes.network.affectedResource'), render: (_value, item) => [item.runtimeId, item.inboundId ? `#${item.inboundId}` : '', item.assetSlot, item.clientId, item.placementId].filter(Boolean).join(' · ') },
      ]} />}
      <Typography.Title level={5}>{t('pages.nodes.network.runtimeClaims')}</Typography.Title>
      <Table rowKey="id" size="small" pagination={false} dataSource={preview.runtimes} columns={[
        { title: t('pages.nodes.network.entryName'), dataIndex: 'id' }, { title: t('pages.nodes.network.core'), dataIndex: 'core' }, { title: t('pages.nodes.network.assignedIdentities'), dataIndex: 'clients' },
      ]} />
      <Typography.Title level={5}>{t('pages.nodes.network.listenerClaims')}</Typography.Title>
      <Table rowKey={item => [item.runtimeId, item.inboundId, item.protocol, item.address, item.port, item.role].join('|')} size="small" pagination={{ pageSize: 15 }} dataSource={preview.listeners} scroll={{ x: 500 }} columns={[
        { title: t('pages.nodes.network.entryName'), dataIndex: 'runtimeId' }, { title: t('pages.nodes.network.sourceInbound'), dataIndex: 'inboundId' },
        { title: t('pages.nodes.network.listen'), render: (_value, item) => `${item.protocol} · ${item.address}:${item.port}` }, { title: t('pages.nodes.network.listenerRole'), dataIndex: 'role' },
      ]} />
      <Typography.Title level={5}>{t('pages.nodes.network.assetClaims')}</Typography.Title>
      <Table rowKey="slot" size="small" pagination={{ pageSize: 10 }} dataSource={preview.assets} scroll={{ x: 500 }} columns={[
        { title: t('pages.nodes.network.assetSlot'), dataIndex: 'slot' }, { title: t('pages.nodes.network.revision'), render: (_value, item) => `${item.assetId} · v${item.revision}` },
        { title: t('pages.nodes.network.digest'), render: (_value, item) => <Typography.Text code copyable>{item.sha256}</Typography.Text> },
      ]} />
    </>}
  </Space>;
}
