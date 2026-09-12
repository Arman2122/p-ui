import { useTranslation } from 'react-i18next';
import { Alert, Form, InputNumber, Modal, Select } from 'antd';
import { FormProvider } from 'react-hook-form';
import { useMutation } from '@tanstack/react-query';
import { z } from 'zod';
import { useZodForm, FormField } from '@/components/form/rhf';
import type { GroupSummary, InboundOption } from '@/schemas/client';
import { HttpUtil } from '@/utils';

const QuotaFormSchema = z.object({
  quotaGiB: z.number().min(0, 'pages.clients.errNotNegative').max(Number.MAX_SAFE_INTEGER / 1073741824, 'telemetry.quotaTooLarge'),
  inboundIds: z.array(z.number()),
});

export default function GroupQuotaModal({ group, inbounds = [], onClose, onSaved }: {
  group: GroupSummary; inbounds?: InboundOption[]; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useTranslation();
  const methods = useZodForm(QuotaFormSchema, { defaultValues: { quotaGiB: (group.quotaBytes ?? 0) / 1073741824, inboundIds: group.sharedInboundIds ?? [] } });
  const dirtyFields = methods.formState.dirtyFields;
  const mutation = useMutation({ mutationFn: async ({ quotaGiB, inboundIds }: z.infer<typeof QuotaFormSchema>) => {
    const msg = await HttpUtil.post('/panel/api/clients/groups/quota',
      { name: group.name, quotaBytes: Math.round(quotaGiB * 1073741824),
        ...(dirtyFields.inboundIds ? { inboundIds } : {}) }, { headers: { 'Content-Type': 'application/json' } });
    if (!msg?.success) throw new Error(msg?.msg || t('somethingWentWrong'));
  }, onSuccess: () => { onSaved(); onClose(); } });
  return <Modal open title={`${t('telemetry.groupQuota')}: ${group.name}`} onCancel={onClose}
    onOk={methods.handleSubmit((values) => mutation.mutate(values))} confirmLoading={mutation.isPending}
    okText={t('save')} cancelText={t('cancel')}>
    <FormProvider {...methods}><Form layout="vertical">
      <FormField name="quotaGiB" label={t('telemetry.quotaGiB')}><InputNumber min={0} style={{ width: '100%' }} /></FormField>
      <FormField name="inboundIds" label={t('telemetry.sharedInbounds')} extra={t('telemetry.sharedInboundsHelp')}>
        <Select mode="multiple" allowClear options={inbounds.map((ib) => ({ value: ib.id, label: `${ib.remark || ib.tag || ib.id} · ${ib.protocol ?? ''}` }))} />
      </FormField>
    </Form></FormProvider>
    <Alert type="info" title={t('telemetry.groupQuotaHelp')} />
    {mutation.error && <Alert type="error" title={mutation.error.message} style={{ marginTop: 12 }} />}
  </Modal>;
}
