import { useState } from 'react';
import { z } from 'zod';
import { Alert, Checkbox, Collapse, Form, Input, InputNumber, Modal, Select } from 'antd';
import { useTranslation } from 'react-i18next';
import JsonEditor from '@/components/form/JsonEditor';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import type { FleetNetworkOperation, FleetNetworkSpec } from '@/generated/zod';
import {
  FleetAssetKindSchema, NetworkInboundIDSchema, NetworkIPv4Schema, NetworkListenSchema, NetworkNameSchema, NetworkPortSchema,
  networkJSONSchema, networkOperation, type NetworkEdit,
} from '@/schemas/forms/fleet-network';

export function networkRule(schema: z.ZodType, message: string) {
  return { validator: async (_rule: unknown, value: unknown) => { if (!schema.safeParse(value).success) throw new Error(message); } };
}

export default function FleetNetworkEditor({ initial, specification, onSave, onClose }: {
  initial: NetworkEdit; specification: FleetNetworkSpec;
  onSave: (operation: FleetNetworkOperation) => Promise<void>; onClose: () => void;
}) {
  const { t } = useTranslation();
  const [form] = Form.useForm<NetworkEdit>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const inbounds = useInboundOptions();
  const listen = Form.useWatch('overrideListen', form);
  const port = Form.useWatch('overridePort', form);
  const hostKeyMode = Form.useWatch('hostKeyMode', form);
  const section = initial.section;
  const invalid = t('pages.nodes.network.invalidField');
  const usedNames = section === 'base' ? specification.bases : section === 'bridge' ? specification.bridges : specification.assetSlots;
  const entryName = NetworkNameSchema.refine(value => Boolean(initial.key) || !Object.hasOwn(usedNames ?? {}, value));
  const save = async (value: NetworkEdit) => {
    setSaving(true);
    setError('');
    try {
      await onSave(networkOperation({ ...initial, ...value }));
      onClose();
    } catch (error) {
      setError(error instanceof z.ZodError ? invalid : error instanceof Error ? error.message : t('somethingWentWrong'));
    } finally { setSaving(false); }
  };
  const json = <Form.Item name="json" label={t('pages.nodes.network.configurationJSON')} rules={[networkRule(networkJSONSchema(section), t('pages.nodes.network.invalidJSON'))]}><JsonEditor value="" minHeight="220px" maxHeight="440px" /></Form.Item>;
  return <Modal open title={t('pages.nodes.network.editSection', { section: t(`pages.nodes.network.sections.${section}`) })} onCancel={() => { if (!saving) onClose(); }} onOk={() => form.submit()} okButtonProps={{ color: 'default', variant: 'solid' }} confirmLoading={saving} cancelButtonProps={{ disabled: saving }} closable={!saving} keyboard={!saving} mask={{ closable: !saving }} width={760} destroyOnHidden>
    {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
    <Form form={form} layout="vertical" initialValues={initial} onFinish={save}>
      {section === 'inbound' && <>
        <Form.Item name="inboundId" label={t('pages.nodes.network.sourceInbound')} rules={[networkRule(NetworkInboundIDSchema, invalid)]}>
          <Select showSearch={{ optionFilterProp: 'label' }} loading={inbounds.isPending} disabled={initial.inboundId != null} options={inbounds.data?.map(item => ({ value: item.id, disabled: !initial.inboundId && specification.inbounds?.[String(item.id)] != null, label: `${item.remark || item.tag || item.protocol || ''} · #${item.id}${item.port ? ` · ${item.port}` : ''}` }))} />
        </Form.Item>
        {inbounds.error && <Alert type="error" title={inbounds.error.message} />}
        <Form.Item name="overrideListen" valuePropName="checked"><Checkbox>{t('pages.nodes.network.overrideListen')}</Checkbox></Form.Item>
        {listen && <Form.Item name="listen" label={t('pages.nodes.network.listen')} extra={t('pages.nodes.network.emptyListen')} rules={[networkRule(NetworkListenSchema, invalid)]}><Input autoComplete="off" /></Form.Item>}
        <Form.Item name="overridePort" valuePropName="checked"><Checkbox>{t('pages.nodes.network.overridePort')}</Checkbox></Form.Item>
        {port && <Form.Item name="port" label={t('pages.nodes.network.port')} rules={[networkRule(NetworkPortSchema, invalid)]}><InputNumber min={1} max={65535} /></Form.Item>}
        <Collapse items={[{ key: 'advanced', label: t('pages.nodes.network.advanced'), children: json }]} />
      </>}
      {section === 'native-network' && <>
        <Alert type="info" showIcon title={t('pages.nodes.network.nativeHint')} style={{ marginBottom: 16 }} />
        <Form.Item name="peerIPv4" label={t('pages.nodes.network.peerIPv4')} rules={[networkRule(NetworkIPv4Schema, invalid)]}><Input autoComplete="off" /></Form.Item>
        <Form.Item name="gatewayIPv4" label={t('pages.nodes.network.gatewayIPv4')} dependencies={['peerIPv4']} rules={[networkRule(NetworkIPv4Schema, invalid), { validator: async (_rule, value: unknown) => { if (value === form.getFieldValue('peerIPv4')) throw new Error(invalid); } }]}><Input autoComplete="off" /></Form.Item>
      </>}
      {section === 'ssh-host-keys' && <>
        <Form.Item name="hostKeyMode" label={t('pages.nodes.network.sshIdentity')}><Select options={[{ value: 'generate', label: t('pages.nodes.network.generateHostKeys') }, { value: 'asset', label: t('pages.nodes.network.useHostKeyAsset') }]} /></Form.Item>
        {hostKeyMode === 'asset' && <Form.Item name="slot" label={t('pages.nodes.network.assetSlot')} rules={[networkRule(NetworkNameSchema, invalid)]}><Select options={Object.entries(specification.assetSlots ?? {}).filter(([, kind]) => kind === 'ssh-host-keys').map(([value]) => ({ value, label: value }))} /></Form.Item>}
      </>}
      {['base', 'bridge', 'asset-slot'].includes(section) && <Form.Item name="key" label={t('pages.nodes.network.entryName')} rules={[networkRule(entryName, invalid)]}><Input maxLength={128} disabled={Boolean(initial.key)} autoComplete="off" /></Form.Item>}
      {section === 'asset-slot' && <Form.Item name="kind" label={t('pages.nodes.network.assetKind')} rules={[networkRule(FleetAssetKindSchema, invalid)]}><Select options={FleetAssetKindSchema.options.map(value => ({ value, label: t(`pages.nodes.network.assetKinds.${value}`) }))} /></Form.Item>}
      {(section === 'base' || section === 'bridge') && <><Alert type="info" title={t('pages.nodes.network.advancedHint')} style={{ marginBottom: 16 }} />{json}</>}
    </Form>
  </Modal>;
}
