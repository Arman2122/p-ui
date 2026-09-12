import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { FormProvider, useWatch } from 'react-hook-form';
import { Alert, Button, Empty, Form, Input, InputNumber, Modal, Select, Space, Switch, Tag, Upload, message } from 'antd';
import { PanelCard } from '@/components/ui/panel';
import { FormField, useZodForm } from '@/components/form/rhf';
import { BackupCreateSchema, BackupScheduleSchema, BackupStatusSchema, type BackupCreateValues, type BackupRecord, type BackupSchedule } from '@/schemas/settings-operations';
import { ClipboardManager, HttpUtil, IntlUtil, SizeFormatter } from '@/utils';
import { backupRestoreCommand } from './backup-restore-command';

const queryKey = ['settings-backups'];

function ScheduleForm({ value, onSaved, telegramAvailable }: { value: BackupSchedule; onSaved: () => Promise<unknown>; telegramAvailable: boolean }) {
  const { t } = useTranslation();
  const methods = useZodForm(BackupScheduleSchema, { defaultValues: value });
  const save = useMutation({ mutationFn: async (schedule: BackupSchedule) => {
    const result = await HttpUtil.post('/panel/api/backups/schedule', schedule);
    if (!result.success) throw new Error(result.msg);
    methods.reset(schedule);
    await onSaved();
  } });
  return <PanelCard title={t('settingsOps.backup.schedule')}>
    <FormProvider {...methods}><Form layout="vertical" onFinish={methods.handleSubmit((v) => save.mutate(v))}>
      <FormField name="enabled" label={t('settingsOps.backup.automatic')} valueProp="checked"><Switch /></FormField>
      <FormField name="cadence" label={t('settingsOps.backup.schedule')}><Select options={[
        { value: 'hourly', label: t('settingsOps.backup.hourly') },
        { value: 'daily', label: t('settingsOps.backup.daily') },
        { value: 'weekly', label: t('settingsOps.backup.weekly') },
      ]} /></FormField>
      <FormField name="keep" label={t('settingsOps.backup.keep')}><InputNumber min={1} max={365} precision={0} /></FormField>
      <Form.Item label={t('settingsOps.backup.encrypt')} extra={t('settingsOps.backup.scheduledEncryptionHelp')}><Switch checked={false} disabled /></Form.Item>
      <FormField name="sendToTelegram" label={t('settingsOps.backup.telegram')} valueProp="checked" extra={t('settingsOps.backup.scheduledTelegramHelp')}><Switch disabled={!telegramAvailable} /></FormField>
      {save.isError && <Alert type="error" title={save.error.message} />}
      <Button htmlType="submit" type="primary" loading={save.isPending} disabled={!methods.formState.isDirty}>{t('save')}</Button>
    </Form></FormProvider>
  </PanelCard>;
}

function CreateBackupForm({ onCreate, busy, telegramAvailable }: { onCreate: (values: BackupCreateValues) => Promise<void>; busy: boolean; telegramAvailable: boolean }) {
  const { t } = useTranslation();
  const methods = useZodForm(BackupCreateSchema, { defaultValues: { encrypt: true, sendToTelegram: false, passphrase: '', confirmation: '' } });
  const encrypt = useWatch({ control: methods.control, name: 'encrypt' });
  return <PanelCard title={t('settingsOps.backup.now')}>
    <FormProvider {...methods}><Form layout="vertical" onFinish={methods.handleSubmit(async (values) => {
      methods.reset({ ...values, passphrase: '', confirmation: '' });
      await onCreate(values);
    })}>
      <FormField name="encrypt" label={t('settingsOps.backup.encrypt')} valueProp="checked" extra={t('settingsOps.backup.encryptionHelp')}><Switch /></FormField>
      {encrypt && <>
        <FormField name="passphrase" label={t('settingsOps.backup.passphrase')}><Input.Password autoComplete="new-password" maxLength={1024} /></FormField>
        <FormField name="confirmation" label={t('settingsOps.backup.confirmPassphrase')}><Input.Password autoComplete="new-password" maxLength={1024} /></FormField>
      </>}
      <FormField name="sendToTelegram" label={t('settingsOps.backup.telegram')} valueProp="checked" extra={t('settingsOps.backup.telegramHelp')}><Switch disabled={!telegramAvailable} /></FormField>
      <Button htmlType="submit" type="primary" disabled={busy} loading={methods.formState.isSubmitting}>{t('settingsOps.backup.now')}</Button>
    </Form></FormProvider>
  </PanelCard>;
}

export default function BackupSection() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [restore, setRestore] = useState<BackupRecord | File | null>(null);
  const [confirmation, setConfirmation] = useState('');
  const [restored, setRestored] = useState(false);
  const [restorePassphrase, setRestorePassphrase] = useState('');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');
  const [messageApi, messageContext] = message.useMessage();
  const [modal, modalContext] = Modal.useModal();
  const query = useQuery({ queryKey, refetchInterval: 5000, queryFn: async () => {
    const result = await HttpUtil.get('/panel/api/backups', undefined, { silentSuccess: true });
    if (!result.success) throw new Error(result.msg);
    return BackupStatusSchema.parse(result.obj);
  } });
  const refresh = () => client.invalidateQueries({ queryKey });
  const createArchive = async (values: BackupCreateValues) => {
    setCreating(true);
    setCreateError('');
    try {
      const result = await HttpUtil.post('/panel/api/backups/create', { encrypt: values.encrypt, passphrase: values.encrypt ? values.passphrase : '', sendToTelegram: values.sendToTelegram }, { timeout: 1_900_000 });
      if (!result.success) throw new Error(result.msg);
    } catch (error) { setCreateError(error instanceof Error ? error.message : String(error)); }
    finally { values.passphrase = ''; values.confirmation = ''; setCreating(false); await refresh(); }
  };
  const telegram = useMutation({ mutationFn: async (id: string) => {
    const result = await HttpUtil.post(`/panel/api/backups/${id}/telegram`, undefined, { timeout: 600_000 });
    if (!result.success) throw new Error(result.msg);
  }, onSettled: refresh });
  const importBackup = useMutation({ mutationFn: async () => {
    if (!restore) return;
    const data = new FormData();
    data.set('confirm', confirmation);
    data.set('passphrase', restorePassphrase);
    setRestorePassphrase('');
    const upload = restore instanceof File;
    if (upload) data.set('file', restore);
    const url = upload ? '/panel/api/backups/upload' : `/panel/api/backups/${restore.id}/restore`;
    const result = await HttpUtil.post(url, data, { timeout: 1_900_000 });
    if (!result.success) throw new Error(result.msg);
    setRestore(null);
    setRestored(true);
    await client.invalidateQueries();
  }, onSettled: refresh });
  const data = query.data;
  const busy = !!data?.busy || creating || importBackup.isPending || telegram.isPending;
  const latest = data?.records.find((r) => r.operation === 'backup' && r.status === 'completed');
  const chooseRestore = (value: BackupRecord | File) => { setRestore(value); setConfirmation(''); setRestorePassphrase(''); importBackup.reset(); };
  return <Space orientation="vertical" size={12} style={{ width: '100%' }}>
    {messageContext}{modalContext}
    <Alert type="info" showIcon title={t('settingsOps.backup.contents')} description={t('settingsOps.backup.contentsHelp')} />
    {query.isError && <Alert type="error" title={query.error.message} action={<Button onClick={() => void query.refetch()}>{t('refresh')}</Button>} />}
    {(createError || importBackup.error || telegram.error) && <Alert type="error" title={createError || importBackup.error?.message || telegram.error?.message} />}
    {restored && <Alert type="warning" showIcon title={t('settingsOps.backup.restored')} />}
    {data && <>
      <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(190px,1fr))' }}>
        <PanelCard title={t('settingsOps.backup.last')}>{latest ? IntlUtil.formatDate(latest.finishedAt) : t('none')}</PanelCard>
        <PanelCard title={t('settingsOps.backup.snapshots')}>{data.records.filter((r) => r.available).length}</PanelCard>
        <PanelCard title={t('settingsOps.backup.next')}>{data.nextRun ? IntlUtil.formatDate(data.nextRun) : t('none')}</PanelCard>
        <PanelCard title={t('settingsOps.backup.destination')}><code dir="ltr" style={{ overflowWrap: 'anywhere' }}>{data.directory}</code></PanelCard>
      </div>
      <Space wrap>
        <Upload accept=".dump,.pui" showUploadList={false} beforeUpload={(file) => { chooseRestore(file); return false; }} disabled={busy}>
          <Button disabled={busy}>{t('settingsOps.backup.upload')}</Button>
        </Upload>
        <Button onClick={() => void query.refetch()}>{t('refresh')}</Button>
        <Button onClick={async () => {
          const copied = await ClipboardManager.copyText(backupRestoreCommand(window.location.origin, window.PUI_BASE_PATH || '/'));
          if (copied) messageApi.success(t('copySuccess')); else messageApi.error(t('copyFail'));
        }}>{t('settingsOps.backup.copyRestoreCommand')}</Button>
        {busy && <Tag color="processing">{t('settingsOps.backup.running')}</Tag>}
      </Space>
      <PanelCard title={t('settingsOps.backup.includes')}>
        <Form layout="vertical">
          <Form.Item label={t('settingsOps.backup.includeCoreConfigs')} extra={t('settingsOps.backup.coreConfigsHelp')}><Switch checked={data.managedCoreConfigs} disabled /></Form.Item>
          <Form.Item label={t('settingsOps.backup.includeTrafficHistory')} extra={t('settingsOps.backup.trafficHistoryHelp')}><Switch checked={data.trafficHistory} disabled /></Form.Item>
        </Form>
      </PanelCard>
      <CreateBackupForm onCreate={createArchive} busy={busy} telegramAvailable={data.telegramAvailable} />
      <ScheduleForm value={data.schedule} onSaved={refresh} telegramAvailable={data.telegramAvailable} />
      <PanelCard title={t('settingsOps.backup.history')}>
        {!data.records.length && <Empty description={t('noData')} />}
        {data.records.map((row) => <div key={row.id} style={{ borderBottom: '1px solid var(--p-border)', padding: '12px 0' }}>
          <Space wrap style={{ display: 'flex', justifyContent: 'space-between' }}>
            <div><strong>{IntlUtil.formatDate(row.startedAt)}</strong><div className="p-sub">
              {row.operation === 'backup' ? t('settingsOps.backup.snapshot') : t('settingsOps.backup.restore')} · {t(`settingsOps.backup.source.${row.source}`, { defaultValue: row.source })} · {SizeFormatter.sizeFormat(row.size)}
            </div></div>
            <Space wrap><Tag color={row.status === 'completed' ? 'green' : row.status === 'running' ? 'processing' : 'red'}>{t(`settingsOps.backup.status.${row.status}`, { defaultValue: row.status })}</Tag>
              {row.operation === 'backup' && <Tag>{t(row.encrypted ? 'settingsOps.backup.encrypted' : 'settingsOps.backup.unencrypted')}</Tag>}
              {row.telegramStatus && row.telegramStatus !== 'not_requested' && <Tag color={row.telegramStatus === 'sent' ? 'green' : 'orange'}>{t(`settingsOps.backup.delivery.${row.telegramStatus}`, { defaultValue: row.telegramStatus })}</Tag>}
              {row.available && <>
                <Button href={`${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/panel/api/backups/${row.id}/download`}>{t('download')}</Button>
                <Button danger disabled={busy} onClick={() => chooseRestore(row)}>{t('settingsOps.backup.restore')}</Button>
                <Button disabled={busy || !data.telegramAvailable} onClick={() => modal.confirm({ title: t('settingsOps.backup.telegram'), content: t(row.encrypted ? 'settingsOps.backup.sendEncryptedConfirm' : 'settingsOps.backup.sendPlainConfirm'), onOk: () => telegram.mutateAsync(row.id) })}>{t('settingsOps.backup.telegram')}</Button>
              </>}
            </Space>
          </Space>
          {row.error && <Alert type="error" title={row.error} />}
          {row.telegramError && <Alert type="error" title={row.telegramError} />}
        </div>)}
      </PanelCard>
    </>}
    <Modal open={!!restore} title={t('settingsOps.backup.restore')} onCancel={() => { if (!importBackup.isPending) { setRestore(null); setRestorePassphrase(''); } }}
      onOk={() => importBackup.mutate()} okText={t('settingsOps.backup.restore')} confirmLoading={importBackup.isPending}
      okButtonProps={{ danger: true, disabled: confirmation !== 'RESTORE' }} cancelButtonProps={{ disabled: importBackup.isPending }} mask={{ closable: false }}>
      <Alert type="warning" showIcon title={t('settingsOps.backup.confirm')} description={t('settingsOps.backup.confirmHelp')} />
      <p><code>{restore instanceof File ? restore.name : restore?.filename}</code></p>
      {(restore instanceof File || restore?.encrypted) && <Form layout="vertical"><Form.Item label={t('settingsOps.backup.passphrase')} extra={t('settingsOps.backup.restorePassphraseHelp')}><Input.Password value={restorePassphrase} onChange={(event) => setRestorePassphrase(event.target.value)} autoComplete="off" maxLength={1024} /></Form.Item></Form>}
      <Input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder="RESTORE" aria-label={t('settingsOps.backup.confirm')} />
      {importBackup.isError && <Alert type="error" title={importBackup.error.message} />}
    </Modal>
  </Space>;
}
