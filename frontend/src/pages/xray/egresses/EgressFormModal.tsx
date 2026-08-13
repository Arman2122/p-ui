import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Form, Input, InputNumber, Modal, Select, Switch } from 'antd';
import { FormProvider, useForm, useWatch } from 'react-hook-form';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { useOutboundTagGroups } from '@/api/queries/useOutboundTags';
import { useEgressMutations } from '@/api/queries/useEgressMutations';
import {
  EGRESS_TYPE,
  EGRESS_TYPES,
  EGRESS_TYPE_UPLINK,
  EgressFormFields,
  EgressFormSchema,
  UplinkSettingsSchema,
  splitAddresses,
  type EgressFormValues,
  type EgressRecord,
} from '@/schemas/api/egress';

interface EgressFormModalProps {
  open: boolean;
  egress: EgressRecord | null;
  onClose: () => void;
}

function defaultValues(): EgressFormValues {
  return {
    id: 0,
    type: EGRESS_TYPE,
    remark: '',
    target: '',
    enable: true,
    privateKey: '',
    address: '',
    mtu: 0,
    publicKey: '',
    endpoint: '',
    presharedKey: '',
    keepAlive: 0,
  };
}

/* An existing row carries its settings as a JSON string. Unreadable settings
   open an empty uplink form rather than throwing the operator out of the edit. */
function uplinkFrom(egress: EgressRecord | null): Partial<EgressFormValues> {
  if (!egress?.settings) return {};
  try {
    const parsed = UplinkSettingsSchema.safeParse(JSON.parse(egress.settings));
    if (!parsed.success) return {};
    const s = parsed.data;
    return {
      privateKey: s.privateKey,
      address: s.address.join('\n'),
      mtu: s.mtu,
      publicKey: s.publicKey,
      endpoint: s.endpoint,
      presharedKey: s.presharedKey,
      keepAlive: s.keepAlive,
    };
  } catch {
    return {};
  }
}

/* The refine decides what a save needs, but a refine cannot mark a field. These
   mirror it per field so an empty one says why instead of a Save that does
   nothing -- and they only run while the uplink fields are mounted, which is
   exactly when they apply. */
const required = (key: string) => ({
  validate: (value: unknown) => (value ? true : key),
});

export default function EgressFormModal({ open, egress, onClose }: EgressFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<EgressFormValues>({ defaultValues: defaultValues() });
  const [submitting, setSubmitting] = useState(false);
  const { add, update } = useEgressMutations();
  const { data: outboundGroups } = useOutboundTagGroups();
  const isUplink = useWatch({ control: methods.control, name: 'type' }) === EGRESS_TYPE_UPLINK;

  /* Blackhole stays in the list on purpose: an egress pointed at it is a
     deliberately dark exit, which the backend accepts as a resolvable target. */
  const targetOptions = useMemo<
    ({ label: string; value: string } | { label: string; options: { label: string; value: string }[] })[]
  >(() => {
    const outbounds = (outboundGroups?.outbounds ?? []).map((tag) => ({ label: tag, value: tag }));
    if (!outboundGroups?.balancers.length) return outbounds;
    return [
      { label: t('pages.xray.Outbounds'), options: outbounds },
      { label: t('pages.xray.Balancers'), options: outboundGroups.balancers.map((tag) => ({ label: tag, value: tag })) },
    ];
  }, [outboundGroups, t]);

  useEffect(() => {
    if (!open) return;
    methods.reset(egress
      ? {
          ...defaultValues(),
          id: egress.id,
          type: egress.type === EGRESS_TYPE_UPLINK ? EGRESS_TYPE_UPLINK : EGRESS_TYPE,
          remark: egress.remark,
          target: egress.target,
          enable: egress.enable,
          ...uplinkFrom(egress),
        }
      : defaultValues());
  }, [open, egress, methods]);

  async function onFinish(values: EgressFormValues) {
    const result = EgressFormSchema.safeParse(values);
    if (!result.success) return;
    const form = result.data;
    const uplink = form.type === EGRESS_TYPE_UPLINK;
    setSubmitting(true);
    try {
      const payload = {
        id: form.id,
        type: form.type,
        enable: form.enable,
        remark: form.remark,
        /* An uplink IS the destination, so it names no target; sending one would
           make it look like a front and fail the backend's own check. */
        target: uplink ? '' : form.target,
        settings: uplink
          ? JSON.stringify({
              privateKey: form.privateKey,
              address: splitAddresses(form.address),
              mtu: form.mtu,
              publicKey: form.publicKey,
              endpoint: form.endpoint,
              presharedKey: form.presharedKey,
              keepAlive: form.keepAlive,
            })
          : (egress?.settings ?? ''),
      };
      const msg = egress ? await update(egress.id, payload) : await add(payload);
      if (msg?.success) onClose();
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Modal
      open={open}
      title={egress ? t('pages.xray.egress.edit') : t('pages.xray.egress.add')}
      confirmLoading={submitting}
      okText={t('save')}
      cancelText={t('cancel')}
      mask={{ closable: false }}
      width="520px"
      onOk={methods.handleSubmit(onFinish)}
      onCancel={() => { if (!submitting) onClose(); }}
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField
            label={t('pages.xray.egress.type')}
            name="type"
            tooltip={t('pages.xray.egress.typeHint')}
          >
            <Select
              disabled={!!egress}
              options={EGRESS_TYPES.map((value) => ({
                value,
                label: t(`pages.xray.egress.types.${value}`),
              }))}
            />
          </FormField>

          <FormField label={t('remark')} name="remark">
            <Input placeholder={t('pages.xray.egress.remarkPlaceholder')} />
          </FormField>

          {!isUplink && (
            <FormField
              label={t('pages.xray.egress.target')}
              name="target"
              tooltip={t('pages.xray.egress.targetHint')}
              required
              rules={{ validate: rhfZodValidate(EgressFormFields.shape.target) }}
            >
              <Select
                showSearch
                placeholder={t('pages.xray.egress.targetPlaceholder')}
                options={targetOptions}
              />
            </FormField>
          )}

          {isUplink && (
            <>
              <FormField
                label={t('pages.xray.egress.endpoint')}
                name="endpoint"
                tooltip={t('pages.xray.egress.endpointHint')}
                required
                rules={required('pages.xray.egress.endpointRequired')}
              >
                <Input id="endpoint" placeholder="us-sfo.example.com:51820" autoComplete="off" />
              </FormField>

              <FormField
                label={t('pages.xray.egress.privateKey')}
                name="privateKey"
                tooltip={t('pages.xray.egress.privateKeyHint')}
                required
                rules={required('pages.xray.egress.privateKeyRequired')}
              >
                <Input.Password id="privateKey" autoComplete="off" />
              </FormField>

              <FormField
                label={t('pages.xray.egress.publicKey')}
                name="publicKey"
                tooltip={t('pages.xray.egress.publicKeyHint')}
                required
                rules={required('pages.xray.egress.publicKeyRequired')}
              >
                <Input id="publicKey" autoComplete="off" />
              </FormField>

              <FormField
                label={t('pages.xray.egress.address')}
                name="address"
                tooltip={t('pages.xray.egress.addressHint')}
                required
                rules={required('pages.xray.egress.addressRequired')}
              >
                <Input.TextArea id="address" rows={2} placeholder="10.14.0.2/32" autoComplete="off" />
              </FormField>

              <FormField
                label={t('pages.xray.egress.presharedKey')}
                name="presharedKey"
                tooltip={t('pages.xray.egress.presharedKeyHint')}
              >
                <Input.Password id="presharedKey" autoComplete="off" />
              </FormField>

              <FormField
                label={t('pages.inbounds.info.mtu')}
                name="mtu"
                tooltip={t('pages.xray.egress.mtuHint')}
              >
                <InputNumber id="mtu" min={0} max={9000} style={{ width: '100%' }} placeholder="1420" />
              </FormField>

              <FormField
                label={t('pages.xray.egress.keepAlive')}
                name="keepAlive"
                tooltip={t('pages.xray.egress.keepAliveHint')}
              >
                <InputNumber id="keepAlive" min={0} max={3600} style={{ width: '100%' }} placeholder="25" />
              </FormField>
            </>
          )}

          <FormField
            label={t('enable')}
            name="enable"
            valueProp="checked"
            extra={t('pages.xray.egress.enableHint')}
          >
            <Switch />
          </FormField>
        </Form>
      </FormProvider>
    </Modal>
  );
}
