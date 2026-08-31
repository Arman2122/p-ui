import { useTranslation } from 'react-i18next';
import { useWatch } from 'react-hook-form';
import { Alert, Input, InputNumber, Select, Switch } from 'antd';

import { HeaderMapEditor } from '@/components/form';
import { FormField } from '@/components/form/rhf';

export default function TunnelFields() {
  const { t } = useTranslation();
  const rewriteAddress = useWatch({ name: 'settings.rewriteAddress' });
  const rewritePort = useWatch({ name: 'settings.rewritePort' });
  const portMap = useWatch({ name: 'settings.portMap' }) as Record<string, unknown> | undefined;
  const followRedirect = useWatch({ name: 'settings.followRedirect' });
  /* With none of these set, dokodemo substitutes 127.0.0.1 and the connection's
     own port, so the inbound forwards to itself. The save refuses it; this says
     so before the operator gets there. F316. */
  const forwardsToItself = !followRedirect && !rewriteAddress && !rewritePort
    && Object.keys(portMap ?? {}).length === 0;
  return (
    <>
      {forwardsToItself && (
        <Alert type="warning" showIcon style={{ marginBottom: 16 }} message={t('pages.inbounds.form.tunnelNoDestination')} />
      )}
      <FormField name={['settings', 'rewriteAddress']} label={t('pages.inbounds.form.rewriteAddress')}>
        <Input />
      </FormField>
      <FormField name={['settings', 'rewritePort']} label={t('pages.inbounds.form.rewritePort')}>
        <InputNumber min={0} max={65535} />
      </FormField>
      <FormField name={['settings', 'allowedNetwork']} label={t('pages.inbounds.form.allowedNetwork')}>
        <Select
          options={[
            { value: 'tcp,udp', label: 'TCP, UDP' },
            { value: 'tcp', label: 'TCP' },
            { value: 'udp', label: 'UDP' },
          ]}
        />
      </FormField>
      <FormField label={t('pages.inbounds.portMap')} name={['settings', 'portMap']}>
        <HeaderMapEditor mode="v1" />
      </FormField>
      <FormField
        name={['settings', 'followRedirect']}
        label={t('pages.inbounds.form.followRedirect')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
    </>
  );
}
