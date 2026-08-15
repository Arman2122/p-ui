import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Form, Input, Modal } from 'antd';
import { FormProvider, useForm } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import {
  EgressFormFields,
  UplinkSettingsSchema,
  uplinkPayload,
  type EgressFormValues,
  type EgressRecord,
} from '@/schemas/api/egress';

import ExitFields from './protocols/exit';

/* An existing row carries its settings as a JSON string. Unreadable settings
   open an empty form rather than throwing the operator out of the edit. */
function valuesFrom(exit: EgressRecord | null): EgressFormValues {
  const base = EgressFormFields.parse({ remark: exit?.remark ?? '', enable: exit?.enable ?? true });
  if (!exit?.settings) return base;
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(exit.settings));
    if (!parsed.success) return base;
    const s = parsed.data;
    return {
      ...base,
      id: exit.id,
      privateKey: s.privateKey,
      address: s.address.join('\n'),
      mtu: s.mtu,
      publicKey: s.publicKey,
      endpoint: s.endpoint,
      presharedKey: s.presharedKey,
      keepAlive: s.keepAlive,
    };
  } catch {
    return base;
  }
}

interface ExitFormModalProps {
  open: boolean;
  exit: EgressRecord | null;
  onClose: () => void;
}

/* Editing only: an exit is created from the outbound picker, where choosing its
   protocol is the same act as choosing vless or trojan. */
export default function ExitFormModal({ open, exit, onClose }: ExitFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<EgressFormValues>({ defaultValues: valuesFrom(null) });
  const [submitting, setSubmitting] = useState(false);
  const { update } = useEgressMutations();

  useEffect(() => {
    if (!open) return;
    methods.reset(valuesFrom(exit));
  }, [open, exit, methods]);

  async function onOk() {
    if (!exit) return;
    if (!(await methods.trigger())) return;
    const parsed = EgressFormFields.safeParse(methods.getValues());
    if (!parsed.success) return;
    setSubmitting(true);
    try {
      const msg = await update(exit.id, { ...uplinkPayload(parsed.data, parsed.data.remark), id: exit.id });
      if (msg?.success) onClose();
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      title={t('pages.xray.egress.edit')}
      confirmLoading={submitting}
      okText={t('save')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      width="520px"
      onOk={onOk}
      onCancel={() => { if (!submitting) onClose(); }}
      destroyOnHidden
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField label={t('remark')} name="remark">
            <Input placeholder={t('pages.xray.egress.remarkPlaceholder')} />
          </FormField>
          <ExitFields />
        </Form>
      </FormProvider>
    </Modal>
  );
}
