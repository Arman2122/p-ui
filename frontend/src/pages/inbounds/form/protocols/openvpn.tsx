import { useTranslation } from 'react-i18next';
import { Alert, Input, Select, Space, Switch } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { OPENVPN_PROTOS } from '@/schemas/protocols/inbound/openvpn';

export default function OpenvpnFields() {
  const { t } = useTranslation();
  const { control, setValue } = useFormContext();
  const redirect = useWatch({ control, name: 'settings.redirectGateway' }) as boolean | undefined;
  const dns = useWatch({ control, name: 'settings.dns' }) as string[] | undefined;
  const serverCert = useWatch({ control, name: 'settings.serverCert' }) as string | undefined;
  return (
    <>
      <FormField name={['settings', 'proto']} label={t('pages.inbounds.form.openvpnProto')} tooltip={t('pages.inbounds.form.openvpnProtoHint')}>
        <Select options={OPENVPN_PROTOS.map((value) => ({ value, label: value.toUpperCase() }))} />
      </FormField>
      <FormField name={['settings', 'subnet']} label={t('pages.inbounds.form.openvpnSubnet')} tooltip={t('pages.inbounds.form.openvpnSubnetHint')}>
        <Input placeholder="10.9.0.0/24" />
      </FormField>
      <FormField name={['settings', 'dns']} label={t('pages.inbounds.form.openvpnDns')} tooltip={t('pages.inbounds.form.openvpnDnsHint')}>
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
      <FormField name={['settings', 'redirectGateway']} label={t('pages.inbounds.form.openvpnRedirect')} tooltip={t('pages.inbounds.form.openvpnRedirectHint')}>
        <Switch checked={redirect !== false} onChange={(v) => setValue('settings.redirectGateway', v, { shouldDirty: true })} />
      </FormField>
      <Space direction="vertical" style={{ width: '100%' }}>
        <Alert
          type="info"
          showIcon
          title={serverCert ? t('pages.inbounds.form.openvpnIdentityMinted') : t('pages.inbounds.form.openvpnIdentityPending')}
        />
        <Alert type="info" showIcon title={t('pages.inbounds.form.openvpnRadius')} />
      </Space>
    </>
  );
}
