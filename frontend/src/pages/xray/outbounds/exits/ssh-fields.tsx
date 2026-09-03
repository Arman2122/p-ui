import { useTranslation } from 'react-i18next';
import { Input, InputNumber } from 'antd';

import { FormField } from '@/components/form/rhf';

/*
An SSH exit: the panel dials a remote SSH server and exposes a loopback SOCKS
port routing rules send TCP to. The host key fingerprint is required — an exit
that trusts whatever answers first is not an exit an operator can vouch for.
*/

function required(key: string) {
  return { validate: (value: unknown) => (value ? true : key) };
}

export default function SshExitFields() {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        label={t('pages.xray.egress.sshHost')}
        name="sshHost"
        tooltip={t('pages.xray.egress.sshHostHint')}
        required
        rules={required('pages.xray.egress.sshHostRequired')}
      >
        <Input id="sshHost" placeholder="exit.example.com" autoComplete="off" />
      </FormField>

      <FormField label={t('pages.xray.egress.sshPort')} name="sshPort">
        <InputNumber id="sshPort" min={1} max={65535} style={{ width: '100%' }} />
      </FormField>

      <FormField
        label={t('pages.xray.egress.sshUser')}
        name="sshUser"
        required
        rules={required('pages.xray.egress.sshUserRequired')}
      >
        <Input id="sshUser" autoComplete="off" />
      </FormField>

      <FormField
        label={t('pages.xray.egress.sshPassword')}
        name="sshPassword"
        tooltip={t('pages.xray.egress.sshAuthHint')}
      >
        <Input.Password id="sshPassword" autoComplete="off" />
      </FormField>

      <FormField
        label={t('pages.xray.egress.sshKey')}
        name="sshKey"
        tooltip={t('pages.xray.egress.sshAuthHint')}
      >
        <Input.TextArea id="sshKey" rows={4} autoComplete="off" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
      </FormField>

      <FormField
        label={t('pages.xray.egress.sshFingerprint')}
        name="sshFingerprint"
        tooltip={t('pages.xray.egress.sshFingerprintHint')}
        required
        rules={required('pages.xray.egress.sshFingerprintRequired')}
      >
        <Input id="sshFingerprint" placeholder="SHA256:…" autoComplete="off" />
      </FormField>
    </>
  );
}
