import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Form, Input, Modal } from 'antd';
import { FormProvider, useForm } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import {
  EgressFormFields,
  type EgressFormValues,
  type EgressRecord,
} from '@/schemas/api/egress';

import { exitModuleForType } from './exits';

interface ExitFormModalProps {
  open: boolean;
  exit: EgressRecord | null;
  onClose: () => void;
}

/* Editing only: an exit is created from the outbound picker, where choosing its
   protocol is the same act as choosing vless or trojan. */
export default function ExitFormModal({ open, exit, onClose }: ExitFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<EgressFormValues>({ defaultValues: EgressFormFields.parse({}) });
  /* Found by the row's stored type, which is the driver's name for it — the kind
     the picker used is not carried on the row. */
  const module = exit ? exitModuleForType(exit.type) : undefined;
  const [submitting, setSubmitting] = useState(false);
  const { update } = useEgressMutations();

  useEffect(() => {
    if (!open) return;
    methods.reset(exit && module ? module.fromRecord(exit) : EgressFormFields.parse({}));
  }, [open, exit, module, methods]);

  async function onOk() {
    if (!exit || !module) return;
    if (!(await methods.trigger())) return;
    const parsed = EgressFormFields.safeParse(methods.getValues());
    if (!parsed.success) return;
    setSubmitting(true);
    try {
      const msg = await update(exit.id, { ...module.toPayload(parsed.data, parsed.data.remark), id: exit.id });
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
          {module && <module.Fields />}
        </Form>
      </FormProvider>
    </Modal>
  );
}
