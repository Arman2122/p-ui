import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Form, Input, InputNumber, Modal, Popconfirm, Table, Typography } from 'antd';
import { FormProvider } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { FormField, useZodForm } from '@/components/form/rhf';
import { PageHeader, PanelCard } from '@/components/ui/panel';
import { resellerGet, resellerKeys, resellerPost } from './api';
import { ResellerPasswordSchema, ResellerTokenFormSchema, ResellerTokenSchema } from './schema';

export default function ResellerAccountPage() {
  const { t } = useTranslation(); const cache = useQueryClient();
  const [secret, setSecret] = useState<string>();
  const password = useZodForm(ResellerPasswordSchema, { defaultValues: { current: '', password: '' } });
  const token = useZodForm(ResellerTokenFormSchema, { defaultValues: { name: '', days: 30 } });
  const query = useQuery({ queryKey: [...resellerKeys, 'tokens'], queryFn: () => resellerGet('/reseller/tokens', z.array(ResellerTokenSchema)) });
  const changePassword = useMutation({ mutationFn: (v: z.infer<typeof ResellerPasswordSchema>) => resellerPost('/reseller/password', v), onSuccess: () => { window.location.href = window.PUI_BASE_PATH || '/'; } });
  const createToken = useMutation({ mutationFn: (v: z.infer<typeof ResellerTokenFormSchema>) => resellerPost('/reseller/tokens', { name: v.name, expiresAt: Date.now() + v.days * 86_400_000 }), onSuccess: async (result) => { setSecret(ResellerTokenSchema.parse(result).token); token.reset(); await cache.invalidateQueries({ queryKey: [...resellerKeys, 'tokens'] }); } });
  const revoke = useMutation({ mutationFn: (id: number) => resellerPost(`/reseller/tokens/${id}/delete`), onSuccess: () => cache.invalidateQueries({ queryKey: [...resellerKeys, 'tokens'] }) });
  const error = query.error || changePassword.error || createToken.error || revoke.error;
  return <><PageHeader title={t('resellers.account')} sub={t('resellers.accountHelp')} />
    {error && <Alert type="error" title={t(error.message)} showIcon />}
    <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,320px),1fr))' }}>
      <PanelCard title={t('resellers.changePassword')}><FormProvider {...password}><Form component="div" layout="vertical">
        <FormField name="current" label={t('resellers.currentPassword')}><Input.Password autoComplete="current-password" /></FormField>
        <FormField name="password" label={t('resellers.newPassword')} extra={t('resellers.passwordRequired')}><Input.Password autoComplete="new-password" /></FormField>
        <Button type="primary" loading={changePassword.isPending} onClick={() => void password.handleSubmit((v) => changePassword.mutate(v))()}>{t('save')}</Button>
      </Form></FormProvider></PanelCard>
      <PanelCard title={t('resellers.newToken')}><FormProvider {...token}><Form component="div" layout="vertical">
        <FormField name="name" label={t('resellers.name')}><Input /></FormField>
        <FormField name="days" label={t('resellers.tokenDays')}><InputNumber min={1} max={365} precision={0} /></FormField>
        <Button type="primary" loading={createToken.isPending} onClick={() => void token.handleSubmit((v) => createToken.mutate(v))()}>{t('resellers.newToken')}</Button>
      </Form></FormProvider></PanelCard>
    </div>
    <PanelCard title={t('resellers.tokens')}><Table rowKey="id" dataSource={query.data} loading={query.isPending} pagination={false} columns={[
      { title: t('resellers.name'), dataIndex: 'name' }, { title: t('resellers.expiry'), render: (_, r) => new Date(r.expiresAt).toLocaleString() },
      { title: t('resellers.actions'), render: (_, r) => <Popconfirm title={t('resellers.revokeConfirm')} onConfirm={() => revoke.mutateAsync(r.id)}><Button danger loading={revoke.isPending}>{t('resellers.revoke')}</Button></Popconfirm> },
    ]} /></PanelCard>
    <Modal open={!!secret} title={t('resellers.tokenCreated')} footer={null} onCancel={() => setSecret(undefined)}><p>{t('resellers.tokenOnce')}</p><Typography.Paragraph copyable={{ text: secret }}><bdi style={{ overflowWrap: 'anywhere' }}>{secret}</bdi></Typography.Paragraph></Modal>
  </>;
}
