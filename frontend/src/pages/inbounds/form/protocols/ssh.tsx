import { useTranslation } from 'react-i18next';
import { Input, InputNumber, Select, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { SSH_TRANSPORTS } from '@/schemas/protocols/inbound/ssh';

export default function SshFields() {
  const { t } = useTranslation();
  const { control } = useFormContext();
  const transport = useWatch({ control, name: 'settings.transport' }) as string | undefined;
  const udpgw = useWatch({ control, name: 'settings.udpgw' }) as boolean | undefined;
  const overWebSocket = transport === 'ws' || transport === 'wss';
  return (
    <>
      <FormField
        name={['settings', 'transport']}
        label={t('pages.inbounds.form.sshTransport')}
        tooltip={t('pages.inbounds.form.sshTransportHint')}
      >
        <Select
          options={SSH_TRANSPORTS.map((value) => ({ value, label: t(`pages.inbounds.form.sshTransport_${value}`) }))}
        />
      </FormField>
      {overWebSocket && (
        <FormField
          name={['settings', 'path']}
          label={t('pages.inbounds.form.sshPath')}
          tooltip={t('pages.inbounds.form.sshPathHint')}
        >
          <Input placeholder="/" />
        </FormField>
      )}
      {transport === 'wss' && (
        <>
          <FormField name={['settings', 'certFile']} label={t('pages.inbounds.form.sshCertFile')}>
            <Input placeholder="/root/cert/example.com/fullchain.pem" />
          </FormField>
          <FormField name={['settings', 'keyFile']} label={t('pages.inbounds.form.sshKeyFile')}>
            <Input placeholder="/root/cert/example.com/privkey.pem" />
          </FormField>
        </>
      )}
      <FormField
        name={['settings', 'banner']}
        label={t('pages.inbounds.form.sshBanner')}
        tooltip={t('pages.inbounds.form.sshBannerHint')}
      >
        <Input allowClear />
      </FormField>
      <FormField
        name={['settings', 'routeThroughXray']}
        label={t('pages.inbounds.form.sshRouteThroughXray')}
        tooltip={t('pages.inbounds.form.sshRouteThroughXrayHint')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['settings', 'udpgw']}
        label={t('pages.inbounds.form.sshUdpgw')}
        tooltip={t('pages.inbounds.form.sshUdpgwHint')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      {udpgw && (
        <FormField
          name={['settings', 'udpgwPort']}
          label={t('pages.inbounds.form.sshUdpgwPort')}
          tooltip={t('pages.inbounds.form.sshUdpgwPortHint')}
        >
          <InputNumber min={1} max={65535} style={{ width: '100%' }} />
        </FormField>
      )}
    </>
  );
}
