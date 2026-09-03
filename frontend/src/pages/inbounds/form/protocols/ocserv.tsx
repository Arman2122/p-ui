import { useTranslation } from 'react-i18next';
import { Alert, Input, Space, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';

export default function OcservFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const redirect = useWatch({ control, name: 'settings.redirectGateway' }) as boolean | undefined;
  const dns = useWatch({ control, name: 'settings.dns' }) as string[] | undefined;
  const camouflage = useWatch({ control, name: 'settings.camouflage' }) as boolean | undefined;
  const serverCert = useWatch({ control, name: 'settings.serverCert' }) as string | undefined;
  return (
    <>
      <FormField name={['settings', 'subnet']} label={t('pages.inbounds.form.ocservSubnet')} tooltip={t('pages.inbounds.form.ocservSubnetHint')}>
        <Input placeholder="10.11.0.0/24" />
      </FormField>
      <FormField name={['settings', 'dns']} label={t('pages.inbounds.form.ocservDns')} tooltip={t('pages.inbounds.form.ocservDnsHint')}>
        <Input
          value={(dns ?? []).join(', ')}
          placeholder="1.1.1.1, 9.9.9.9"
          onChange={(e) =>
            setValue(
              'settings.dns',
              e.target.value.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean),
              { shouldDirty: true },
            )
          }
        />
      </FormField>
      <FormField name={['settings', 'redirectGateway']} label={t('pages.inbounds.form.ocservRedirect')} tooltip={t('pages.inbounds.form.ocservRedirectHint')}>
        <Switch checked={redirect !== false} onChange={(v) => setValue('settings.redirectGateway', v, { shouldDirty: true })} />
      </FormField>
      <FormField name={['settings', 'camouflage']} label={t('pages.inbounds.form.ocservCamouflage')} tooltip={t('pages.inbounds.form.ocservCamouflageHint')}>
        <Switch checked={camouflage === true} onChange={(v) => setValue('settings.camouflage', v, { shouldDirty: true })} />
      </FormField>
      <Space direction="vertical" style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          title={serverCert ? t('pages.inbounds.form.ocservIdentityMinted') : t('pages.inbounds.form.ocservIdentityPending')}
        />
        <Alert type="info" showIcon title={t('pages.inbounds.form.ocservRadius')} />
      </Space>
    </>
  );
}
