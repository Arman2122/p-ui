import { useTranslation } from 'react-i18next';
import { Input } from 'antd';

import { FormField } from '@/components/form/rhf';

/*
An OpenConnect exit: the panel's openconnect client towards an AnyConnect or
ocserv server. A self-signed server is pinned by the pin-sha256 openconnect
prints; a publicly signed one needs no pin and is checked against the system's CAs.
*/

function required(key: string) {
  return { validate: (value: unknown) => (value ? true : key) };
}

export default function OcservExitFields() {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        label={t('pages.xray.egress.ocServer')}
        name="ocServer"
        tooltip={t('pages.xray.egress.ocServerHint')}
        required
        rules={required('pages.xray.egress.ocServerRequired')}
      >
        <Input id="ocServer" placeholder="vpn.example.net:443" autoComplete="off" />
      </FormField>

      <FormField
        label={t('pages.xray.egress.ocUser')}
        name="ocUser"
        required
        rules={required('pages.xray.egress.ocUserRequired')}
      >
        <Input id="ocUser" autoComplete="off" />
      </FormField>

      <FormField
        label={t('pages.xray.egress.ocPassword')}
        name="ocPassword"
        required
        rules={required('pages.xray.egress.ocPasswordRequired')}
      >
        <Input.Password id="ocPassword" autoComplete="off" />
      </FormField>

      <FormField label={t('pages.xray.egress.ocPin')} name="ocPin" tooltip={t('pages.xray.egress.ocPinHint')}>
        <Input id="ocPin" placeholder="pin-sha256:…" autoComplete="off" />
      </FormField>

      <FormField label={t('pages.xray.egress.ocGroup')} name="ocGroup" tooltip={t('pages.xray.egress.ocGroupHint')}>
        <Input id="ocGroup" autoComplete="off" />
      </FormField>

      <FormField label={t('pages.xray.egress.ocCamouflage')} name="ocCamouflageSecret" tooltip={t('pages.xray.egress.ocCamouflageHint')}>
        <Input id="ocCamouflageSecret" autoComplete="off" />
      </FormField>
    </>
  );
}
