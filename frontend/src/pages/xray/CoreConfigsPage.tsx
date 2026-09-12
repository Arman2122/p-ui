import { useState, type ReactNode } from 'react';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Modal, Select, Space, Spin, Tag } from 'antd';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { CoreConfigSchema } from '@/schemas/settings-operations';
import { InboundDetailSchema } from '@/schemas/inbound';
import { DBInbound } from '@/models/dbinbound';
import { HttpUtil } from '@/utils';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { JsonEditor } from '@/components/form';
import InboundFormModal from '@/pages/inbounds/form/InboundFormModal';

export function downloadConfig(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function storedJSON(value: string): unknown {
  try { return JSON.parse(value || '{}') as unknown; } catch { return value; }
}

function ManagedCoreConfig({ id }: { id: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [selected, setSelected] = useState<number>();
  const [editing, setEditing] = useState<DBInbound | null>(null);
  const [modal, modalContext] = Modal.useModal();
  const query = useQuery({ queryKey: ['core-config', id], queryFn: async () => {
    const result = await HttpUtil.get(`/panel/api/core-config/${encodeURIComponent(id)}`, undefined, { silentSuccess: true });
    if (!result.success) throw new Error(result.msg);
    return CoreConfigSchema.parse(result.obj);
  } });
  const instance = query.data?.instances.find((row) => row.id === selected) ?? query.data?.instances[0];
  const edit = useMutation({ mutationFn: async () => {
    if (!instance) return;
    const result = await HttpUtil.get(`/panel/api/inbounds/get/${instance.id}`);
    if (!result.success) throw new Error(result.msg);
    setEditing(new DBInbound(InboundDetailSchema.parse(result.obj)));
  } });
  const validate = useMutation({ mutationFn: async () => {
    if (!instance) return;
    const result = await HttpUtil.post(`/panel/api/core-config/${encodeURIComponent(id)}/validate`, { kind: instance.kind, document: instance.settings });
    if (!result.success) throw new Error(result.msg);
  } });
  const restart = useMutation({ mutationFn: async () => {
    const result = await HttpUtil.post(`/panel/api/cores/${encodeURIComponent(id)}/restart`);
    if (!result.success) throw new Error(result.msg);
    await client.invalidateQueries({ queryKey: ['core-config', id] });
  } });
  const document = instance ? JSON.stringify({
    listen: instance.listen, port: instance.port, protocol: instance.kind, tag: instance.tag,
    settings: storedJSON(instance.settings),
    streamSettings: storedJSON(instance.streamSettings),
    sniffing: storedJSON(instance.sniffing),
  }, null, 2) : '';
  return <div className="p-network-page">
    {modalContext}
    <div style={{ display: 'flex', justifyContent: 'flex-end' }}><Button danger loading={restart.isPending} onClick={() => modal.confirm({
      title: t('settingsOps.core.restart', { core: id }), content: t('settingsOps.core.restartHelp'),
      onOk: () => restart.mutateAsync(), okButtonProps: { danger: true },
    })}>{t('settingsOps.core.restart', { core: id })}</Button></div>
    <Alert type="info" showIcon title={t('settingsOps.core.managed')} description={t('settingsOps.core.managedHelp')} />
    {(query.error || edit.error || restart.error) && <Alert type="error" title={(query.error || edit.error || restart.error)?.message} action={<Button onClick={() => void query.refetch()}>{t('refresh')}</Button>} />}
    <Spin spinning={query.isPending}>
      {query.data && <PanelCard title={t(query.data.titleKey)}>
        {!query.data.instances.length ? <Empty description={t('settingsOps.core.empty')} /> : <>
          <Space wrap style={{ marginBottom: 12 }}>
            <Select aria-label={t('settingsOps.core.instanceSelect')} style={{ minWidth: 220 }} value={instance?.id} onChange={(value) => { setSelected(value); validate.reset(); }} options={query.data.instances.map((row) => ({ value: row.id, label: `${row.remark || row.tag} · ${row.kind} · ${row.port}` }))} />
            <Button loading={edit.isPending} type="primary" onClick={() => edit.mutate()}>{t('edit')}</Button>
            <Button disabled={!query.data.instanceValidation} loading={validate.isPending} onClick={() => validate.mutate()}>{t('settingsOps.core.validate')}</Button>
            <Button onClick={() => downloadConfig(`${id}-${instance?.id}.json`, document)}>{t('download')}</Button>
            <Tag>{t('settingsOps.core.managedCount', { count: query.data.instances.length })}</Tag>
          </Space>
          {validate.isError && <Alert type="error" title={validate.error.message} />}
          {validate.isSuccess && <Alert type="success" title={t('settingsOps.core.validated')} />}
          {!query.data.instanceValidation && <Alert type="info" title={t('settingsOps.core.noValidator')} />}
          <JsonEditor value={document} readOnly minHeight="420px" maxHeight="720px" />
        </>}
      </PanelCard>}
    </Spin>
    {editing && <InboundFormModal open mode="edit" dbInbound={editing} dbInbounds={query.data?.instances.map((row) => new DBInbound({ id: row.id, protocol: row.kind, settings: row.settings, tag: row.tag })) ?? []}
      onClose={() => setEditing(null)} onSaved={() => { validate.reset(); void client.invalidateQueries({ queryKey: ['core-config', id] }); }} />}
  </div>;
}

export default function CoreConfigsPage({ xray }: { xray: ReactNode }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const id = params.get('core') || 'xray';
  const cores = useCoresQuery();
  const options = cores.data?.map((row) => ({ value: row.id, label: t(row.titleKey) })) ?? [{ value: 'xray', label: 'Xray' }];
  return <>
    <PageHeader title={t('menu.xray')} sub={t('pages.xray.subtitle')} />
    <div className="p-card" style={{ padding: 12 }}>
      <Space wrap><span>{t('settingsOps.core.select')}</span><Select aria-label={t('settingsOps.core.select')} value={id} options={options}
        onChange={(core) => setParams((previous) => { previous.set('core', core); return previous; })} style={{ minWidth: 200 }} /></Space>
      {cores.isError && <Alert type="error" title={cores.error.message} />}
    </div>
    {id === 'xray' ? xray : <ManagedCoreConfig key={id} id={id} />}
  </>;
}
