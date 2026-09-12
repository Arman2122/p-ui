import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AutoComplete, Button, Form, Input, InputNumber, Modal, Select, Space, Switch, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import { FormProvider, useForm, useWatch } from 'react-hook-form';

import { bulkClientSubId } from '@/lib/clients/bulk-subid';
import { RandomUtil, SizeFormatter } from '@/utils';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { supportsMultipleClients } from '@/lib/inbounds/multi-client';
import { TLS_FLOW_CONTROL } from '@/schemas/primitives';
import { DateTimePicker, SelectAllClearButtons } from '@/components/form';
import { FormField } from '@/components/form/rhf';
import { IpLimitControl } from '@/components/clients/IpLimitControl';
import { useClients, type InboundOption } from '@/hooks/useClients';
import { useIpLimitStatusQuery, getLimitIpNotice } from '@/api/queries/useIpLimitStatusQuery';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { ipLimitFieldState } from '@/lib/cores/ip-limit';
import { ClientBulkAddFormSchema, type ClientBulkAddFormValues } from '@/schemas/client';

const FLOW_OPTIONS = Object.values(TLS_FLOW_CONTROL);

const EMPTY: ClientBulkAddFormValues = {
  emailMethod: 0,
  firstNum: 1,
  lastNum: 1,
  emailPrefix: '',
  emailPostfix: '',
  quantity: 1,
  subId: '',
  group: '',
  comment: '',
  flow: '',
  limitIp: 0,
  totalGB: 0,
  expiryTime: 0,
  reset: 0,
  inboundIds: [],
};

interface ClientBulkAddModalProps {
  open: boolean;
  inbounds: InboundOption[];
  groups?: string[];
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
}

export default function ClientBulkAddModal({
  open,
  inbounds,
  groups = [],
  onOpenChange,
  onSaved,
}: ClientBulkAddModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const { bulkCreate } = useClients({ list: false });
  const plans = usePoliciesQuery({ enabled: open });
  const [policyId, setPolicyId] = useState(0);
  const [overridePlan, setOverridePlan] = useState(false);
  const selectedPlan = plans.data?.find((plan) => plan.id === policyId);
  const quotaManaged = policyId > 0 && selectedPlan?.quotaBytes != null && !overridePlan;
  const expiryManaged = policyId > 0 && selectedPlan?.durationDays != null && !overridePlan;
  const ipManaged = policyId > 0 && selectedPlan?.limitIp != null && !overridePlan;
  const inboundsManaged = policyId > 0 && !!selectedPlan?.inboundIds?.length && !overridePlan;

  const methods = useForm<ClientBulkAddFormValues>({ defaultValues: EMPTY });
  const inboundIds = useWatch({ control: methods.control, name: 'inboundIds' });
  const emailMethod = useWatch({ control: methods.control, name: 'emailMethod' });
  const firstNum = useWatch({ control: methods.control, name: 'firstNum' });
  const flow = useWatch({ control: methods.control, name: 'flow' });
  const expiryTime = useWatch({ control: methods.control, name: 'expiryTime' });
  const subId = useWatch({ control: methods.control, name: 'subId' });
  const limitIp = useWatch({ control: methods.control, name: 'limitIp' });
  const [delayedStart, setDelayedStart] = useState(false);
  const [saving, setSaving] = useState(false);
  const ipLimit = useIpLimitStatusQuery();
  const coresQuery = useCoresQuery();
  const cores = coresQuery.data?.length ? coresQuery.data : undefined;
  /* The same two reasons the single-client form knows: no actuator on the
     host, or an inbound whose core cannot report the sessions a cap needs. */
  const attachedKinds = useMemo(() => {
    const kinds: string[] = [];
    for (const id of inboundIds || []) {
      const ib = (inbounds || []).find((row) => row.id === id);
      if (ib?.protocol) kinds.push(ib.protocol);
    }
    return kinds;
  }, [inboundIds, inbounds]);
  const { disabled: limitIpDisabled, notice: limitIpNotice } = useMemo(
    () => ipLimitFieldState(cores, attachedKinds, ipLimit.enabled && ipLimit.enforceable, getLimitIpNotice(ipLimit, t), t),
    [cores, attachedKinds, ipLimit, t],
  );

  useEffect(() => {
    if (!open) return;

    methods.reset(EMPTY);
    setPolicyId(0);
    setOverridePlan(false);
    setDelayedStart(false);

  }, [open, methods]);

  const flowCapableIds = useMemo(() => {
    const ids = new Set<number>();
    for (const row of inbounds || []) {
      if (row?.tlsFlowCapable) ids.add(row.id);
    }
    return ids;
  }, [inbounds]);

  const showFlow = useMemo(
    () => (inboundIds || []).some((id) => flowCapableIds.has(id)),
    [inboundIds, flowCapableIds],
  );

  const ss2022Method = useMemo(() => {
    for (const id of inboundIds || []) {
      const ib = (inbounds || []).find((row) => row.id === id);
      const method = ib?.ssMethod;
      if (method && method.substring(0, 4) === '2022') return method;
    }
    return '';
  }, [inboundIds, inbounds]);

  useEffect(() => {
    if (!showFlow && flow) {

      methods.setValue('flow', '');
    }
  }, [showFlow, flow, methods]);

  const inboundOptions = useMemo(
    () => (inbounds || [])
      .filter((ib) => supportsMultipleClients(ib.protocol))
      .map((ib) => ({
        label: formatInboundLabel(ib.tag, ib.remark),
        value: ib.id,
      })),
    [inbounds],
  );

  const watchedReset = useWatch({ control: methods.control, name: 'reset' });
  const watchedExpiry = useWatch({ control: methods.control, name: 'expiryTime' });
  const renewWithoutExpiry = (Number(watchedReset) || 0) > 0 && (Number(watchedExpiry) || 0) === 0;

  const expiryDate = useMemo<Dayjs | null>(
    () => (expiryTime > 0 ? dayjs(expiryTime) : null),
    [expiryTime],
  );

  const delayedExpireDays = expiryTime < 0 ? expiryTime / -86400000 : 0;

  function buildEmails(values: ClientBulkAddFormValues): string[] {
    const method = values.emailMethod;
    const out: string[] = [];
    let start: number;
    let end: number;
    if (method > 1) {
      start = values.firstNum;
      end = values.lastNum + 1;
    } else {
      start = 0;
      end = values.quantity;
    }
    const prefix = method > 0 && values.emailPrefix.length > 0 ? values.emailPrefix : '';
    const useNum = method > 1;
    const postfix = method > 2 && values.emailPostfix.length > 0 ? values.emailPostfix : '';
    for (let i = start; i < end; i++) {
      let email = '';
      if (method !== 4) email = RandomUtil.randomLowerAndNum(10);
      email += useNum ? prefix + String(i) + postfix : prefix + postfix;
      out.push(email);
    }
    return out;
  }

  async function submit() {
    if (policyId > 0 && (!selectedPlan || selectedPlan.archived)) {
      messageApi.error(t('telemetry.planUnavailable'));
      return;
    }
    const current = methods.getValues();
    const validated = ClientBulkAddFormSchema.safeParse(current);
    if (!validated.success) {
      messageApi.error(t(validated.error.issues[0]?.message ?? 'somethingWentWrong'));
      return;
    }
    const emails = buildEmails(current);
    /* A last number below the first yields nothing. The InputNumber min only
       guards one order of typing, so this returned in silence. */
    if (emails.length === 0) {
      messageApi.error(t('pages.clients.bulkAddNoEmails'));
      return;
    }

    setSaving(true);
    try {
      const payloads = emails.map((email) => ({
        ...(policyId > 0 ? { policyId, ...(overridePlan ? { planOverrides: { quota: true, expiry: true, limitIp: true, inbounds: true } } : {}) } : {}),
        client: {
          email,
          subId: bulkClientSubId(current.subId, emails.length),
          id: RandomUtil.randomUUID(),
          password: ss2022Method
            ? RandomUtil.randomShadowsocksPassword(ss2022Method)
            : RandomUtil.randomLowerAndNum(16),
          auth: RandomUtil.randomLowerAndNum(16),
          flow: showFlow ? (current.flow || '') : '',
          totalGB: Math.round((current.totalGB || 0) * SizeFormatter.ONE_GB),
          expiryTime: current.expiryTime,
          reset: Number(current.reset) || 0,
          limitIp: Number(current.limitIp) || 0,
          group: current.group,
          comment: current.comment,
          enable: true,
        },
        inboundIds: current.inboundIds,
      }));
      const msg = await bulkCreate(payloads);
      const ok = msg?.obj?.created ?? 0;
      const skipped = msg?.obj?.skipped ?? [];
      const failed = skipped.length;
      const firstError = skipped[0]?.reason ?? msg?.msg ?? '';
      if (failed === 0 && msg?.success) {
        messageApi.success(t('pages.clients.toasts.bulkCreated', { count: ok }));
      } else {
        messageApi.warning(firstError
          ? `${t('pages.clients.toasts.bulkCreatedMixed', { ok, failed })} — ${firstError}`
          : t('pages.clients.toasts.bulkCreatedMixed', { ok, failed }));
      }
      onSaved?.();
      onOpenChange(false);
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      {messageContextHolder}
      <Modal
        open={open}
        title={t('pages.clients.bulk')}
        okText={t('create')}
        cancelText={t('close')}
        confirmLoading={saving}
        mask={{ closable: false }}
        width={640}
        onOk={submit}
        onCancel={() => onOpenChange(false)}
      >
        <FormProvider {...methods}>
          <Form colon={false} labelCol={{ sm: { span: 8 } }} wrapperCol={{ sm: { span: 14 } }}>
            <Form.Item label={t('telemetry.usePlan')}>
              <Select value={policyId} loading={plans.isPending} onChange={(id) => {
                setPolicyId(id); setOverridePlan(false);
                const plan = plans.data?.find((p) => p.id === id);
                if (!plan) return;
                if (plan.quotaBytes != null) methods.setValue('totalGB', plan.quotaBytes / 1073741824);
                if (plan.limitIp != null) methods.setValue('limitIp', plan.limitIp);
                if (plan.inboundIds?.length) methods.setValue('inboundIds', plan.inboundIds);
                if (plan.durationDays != null) {
                  setDelayedStart(plan.startOnFirstUse && plan.durationDays > 0);
                  methods.setValue('expiryTime', !plan.durationDays ? 0 : plan.startOnFirstUse ? -plan.durationDays * 86400000 : Date.now() + plan.durationDays * 86400000);
                  methods.setValue('reset', plan.autoRenewDays);
                }
              }} options={[{ value: 0, label: t('telemetry.customPlan') }, ...(plans.data ?? []).filter((p) => !p.archived).map((p) => ({ value: p.id, label: p.name }))]} />
            </Form.Item>
            {policyId > 0 && <Form.Item label={t('telemetry.bulkOverride')}><Switch checked={overridePlan} onChange={setOverridePlan} /></Form.Item>}
            <Form.Item label={t('pages.clients.attachedInbounds')} required>
              {!inboundsManaged && <SelectAllClearButtons
                options={inboundOptions}
                value={inboundIds}
                onChange={(v) => methods.setValue('inboundIds', v)}
              />}
              <Select
                disabled={inboundsManaged}
                mode="multiple"
                value={inboundIds}
                onChange={(v) => methods.setValue('inboundIds', v)}
                options={inboundOptions}
                placeholder={t('pages.clients.selectInbound')}
                showSearch={{
                  filterOption: (input, option) => ((option?.label as string) || '').toLowerCase().includes(input.toLowerCase()),
                }}
              />
            </Form.Item>

            <FormField name="emailMethod" label={t('pages.clients.method')}>
              <Select
                options={[
                  { value: 0, label: 'Random' },
                  { value: 1, label: 'Random + Prefix' },
                  { value: 2, label: 'Random + Prefix + Num' },
                  { value: 3, label: 'Random + Prefix + Num + Postfix' },
                  { value: 4, label: 'Prefix + Num + Postfix' },
                ]}
              />
            </FormField>

            {emailMethod > 1 && (
              <>
                <FormField name="firstNum" label={t('pages.clients.first')} transform={{ output: (v) => Number(v) || 1 }}>
                  <InputNumber min={1} />
                </FormField>
                <FormField name="lastNum" label={t('pages.clients.last')} transform={{ output: (v) => Number(v) || 1 }}>
                  <InputNumber min={firstNum} />
                </FormField>
              </>
            )}
            {emailMethod > 0 && (
              <FormField name="emailPrefix" label={t('pages.clients.prefix')}>
                <Input />
              </FormField>
            )}
            {emailMethod > 2 && (
              <FormField name="emailPostfix" label={t('pages.clients.postfix')}>
                <Input />
              </FormField>
            )}
            {emailMethod < 2 && (
              <FormField name="quantity" label={t('pages.clients.clientCount')} transform={{ output: (v) => Number(v) || 1 }}>
                <InputNumber min={1} max={1000} />
              </FormField>
            )}

            <Form.Item label={t('pages.clients.subId')}>
              <Space.Compact style={{ display: 'flex' }}>
                <Input
                  value={subId}
                  onChange={(e) => methods.setValue('subId', e.target.value)}
                  style={{ flex: 1 }}
                />
                <Button
                  aria-label={t('regenerate')}
                  icon={<ReloadOutlined />}
                  onClick={() => methods.setValue('subId', RandomUtil.randomLowerAndNum(16))}
                />
              </Space.Compact>
            </Form.Item>

            <FormField
              name="group"
              label={t('pages.clients.group')}
              tooltip={t('pages.clients.groupDesc')}
              transform={{ output: (v) => v ?? '' }}
            >
              <AutoComplete
                placeholder={t('pages.clients.groupPlaceholder')}
                options={groups.map((g) => ({ value: g }))}
                allowClear
              />
            </FormField>

            <FormField name="comment" label={t('comment')}>
              <Input />
            </FormField>

            {showFlow && (
              <FormField name="flow" label={t('pages.clients.flow')}>
                <Select
                  style={{ width: 220 }}
                  options={[
                    { value: '', label: t('none') },
                    ...FLOW_OPTIONS.map((k) => ({ value: k, label: k })),
                  ]}
                />
              </FormField>
            )}

            <Form.Item label={t('pages.clients.limitIp')} tooltip={t('pages.clients.limitIpDesc')}>
              <IpLimitControl
                value={limitIp}
                disabled={limitIpDisabled || ipManaged}
                notice={limitIpNotice || undefined}
                onChange={(v) => methods.setValue('limitIp', v)}
              />
            </Form.Item>

            <FormField name="totalGB" label={t('pages.clients.totalGB')} transform={{ output: (v) => Number(v) || 0 }}>
              <InputNumber disabled={quotaManaged} min={0} step={1} />
            </FormField>

            <Form.Item label={t('pages.clients.delayedStart')}>
              <Switch
                disabled={expiryManaged}
                checked={delayedStart}
                onClick={() => { setDelayedStart(!delayedStart); methods.setValue('expiryTime', 0); }}
              />
            </Form.Item>

            {delayedStart ? (
              <Form.Item label={t('pages.clients.expireDays')}>
                <InputNumber
                  disabled={expiryManaged}
                  value={delayedExpireDays}
                  min={0}
                  onChange={(v) => methods.setValue('expiryTime', -86400000 * (Number(v) || 0))}
                />
              </Form.Item>
            ) : (
              <Form.Item label={t('pages.inbounds.expireDate')}>
                <DateTimePicker
                  disabled={expiryManaged}
                  value={expiryDate}
                  onChange={(next) => methods.setValue('expiryTime', next ? next.valueOf() : 0)}
                />
              </Form.Item>
            )}

            <FormField
              name="reset"
              label={t('pages.clients.renew')}
              tooltip={t('pages.clients.renewDesc')}
              extra={renewWithoutExpiry ? t('pages.clients.renewNeedsExpiry') : undefined}
              transform={{ output: (v) => Number(v) || 0 }}
            >
              <InputNumber disabled={expiryManaged} min={0} />
            </FormField>
          </Form>
        </FormProvider>
      </Modal>
    </>
  );
}
