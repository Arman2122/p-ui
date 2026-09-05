import { useTranslation } from 'react-i18next';
import { Alert, Form, Input } from 'antd';

import { FormField } from '@/components/form/rhf';

/*
An OpenVPN exit: the provider's .ovpn pasted whole, plus the login it asks for.
The panel takes the profile under its control (device, routes, DNS, scripts),
so the operator pastes what the provider handed out and nothing else.
*/

function required(key: string) {
  return { validate: (value: unknown) => (value ? true : key) };
}

export default function OpenvpnExitFields() {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        label={t('pages.xray.egress.ovpnProfile')}
        name="ovpnProfile"
        tooltip={t('pages.xray.egress.ovpnProfileHint')}
        required
        rules={required('pages.xray.egress.ovpnProfileRequired')}
      >
        <Input.TextArea
          id="ovpnProfile"
          rows={10}
          autoComplete="off"
          spellCheck={false}
          style={{ fontFamily: 'monospace' }}
          placeholder={'client\ndev tun\nproto udp\nremote vpn.example.net 1194\n…\n<ca>\n…\n</ca>'}
        />
      </FormField>

      <FormField label={t('pages.xray.egress.ovpnUser')} name="ovpnUser" tooltip={t('pages.xray.egress.ovpnAuthHint')}>
        <Input id="ovpnUser" autoComplete="off" />
      </FormField>

      <FormField label={t('pages.xray.egress.ovpnPassword')} name="ovpnPassword" tooltip={t('pages.xray.egress.ovpnAuthHint')}>
        <Input.Password id="ovpnPassword" autoComplete="off" />
      </FormField>

      <Form.Item>
        <Alert type="info" showIcon title={t('pages.xray.egress.ovpnNote')} />
      </Form.Item>
    </>
  );
}
