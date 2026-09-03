import { useTranslation } from 'react-i18next';
import { Alert, Input, Select, Space, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { TUIC_CONGESTION_CONTROLS } from '@/schemas/protocols/inbound/tuic';

/* The fields every sing-box kind shares: TLS is the protocol, so the pair and
   the name the client verifies sit on the inbound itself. */
function TlsFields() {
  const { t } = useTranslation();
  return (
    <>
      <FormField name={['settings', 'certFile']} label={t('pages.inbounds.form.singboxCertFile')}>
        <Input placeholder="/root/cert/example.com/fullchain.pem" />
      </FormField>
      <FormField name={['settings', 'keyFile']} label={t('pages.inbounds.form.singboxKeyFile')}>
        <Input placeholder="/root/cert/example.com/privkey.pem" />
      </FormField>
      <FormField
        name={['settings', 'serverName']}
        label={t('pages.inbounds.form.singboxServerName')}
        tooltip={t('pages.inbounds.form.singboxServerNameHint')}
      >
        <Input placeholder="example.com" allowClear />
      </FormField>
    </>
  );
}

function SingboxNotes() {
  const { t } = useTranslation();
  return (
    <Space direction="vertical" style={{ width: '100%' }}>
      <Alert type="info" showIcon title={t('pages.inbounds.form.singboxAppliesLater')} />
      <Alert type="warning" showIcon title={t('pages.inbounds.form.singboxNoSpeedLimit')} />
    </Space>
  );
}

export function TuicFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const zeroRtt = useWatch({ control, name: 'settings.zeroRttHandshake' }) as boolean | undefined;
  return (
    <>
      <TlsFields />
      <FormField
        name={['settings', 'congestionControl']}
        label={t('pages.inbounds.form.tuicCongestion')}
        tooltip={t('pages.inbounds.form.tuicCongestionHint')}
      >
        <Select options={TUIC_CONGESTION_CONTROLS.map((value) => ({ value, label: value }))} />
      </FormField>
      <FormField
        name={['settings', 'zeroRttHandshake']}
        label={t('pages.inbounds.form.tuicZeroRtt')}
        tooltip={t('pages.inbounds.form.tuicZeroRttHint')}
      >
        <Switch checked={!!zeroRtt} onChange={(v) => setValue('settings.zeroRttHandshake', v, { shouldDirty: true })} />
      </FormField>
      <SingboxNotes />
    </>
  );
}

export function AnytlsFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const padding = useWatch({ control, name: 'settings.paddingScheme' }) as string[] | undefined;
  return (
    <>
      <TlsFields />
      <FormField
        name={['settings', 'paddingScheme']}
        label={t('pages.inbounds.form.anytlsPadding')}
        tooltip={t('pages.inbounds.form.anytlsPaddingHint')}
      >
        <Input.TextArea
          rows={3}
          value={(padding ?? []).join('\n')}
          onChange={(e) =>
            setValue(
              'settings.paddingScheme',
              e.target.value.split('\n').map((line) => line.trim()).filter(Boolean),
              { shouldDirty: true },
            )
          }
        />
      </FormField>
      <SingboxNotes />
    </>
  );
}
