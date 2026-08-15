import { useTranslation } from 'react-i18next';
import { Input, InputNumber } from 'antd';

import { FormField } from '@/components/form/rhf';

/*
The fields a kernel-level exit needs, named as a provider's .conf names them so
an operator pasting from Mullvad or Surfshark recognises each one.
*/

function required(key: string) {
  return { validate: (value: unknown) => (value ? true : key) };
}

export default function ExitFields() {
  const { t } = useTranslation();
  return (
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
  );
}
