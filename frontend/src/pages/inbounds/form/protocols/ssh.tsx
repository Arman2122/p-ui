import { useTranslation } from 'react-i18next';
import { Alert, Input, Select } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { SSH_TRANSPORTS } from '@/schemas/protocols/inbound/ssh';

export default function SshFields() {
  const { t } = useTranslation();
  const { control } = useFormContext();
  const transport = useWatch({ control, name: 'settings.transport' }) as string | undefined;
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
      <Alert type="info" showIcon title={t('pages.inbounds.form.sshTcpOnly')} />
    </>
  );
}
