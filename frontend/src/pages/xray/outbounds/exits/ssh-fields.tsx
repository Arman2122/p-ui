import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, InputNumber, message } from 'antd';
import { useFormContext } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';
import { SSHHostKeySchema } from '@/generated/zod';
import { HttpUtil } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';

/*
An SSH exit: the panel dials a remote SSH server and exposes a loopback SOCKS
port routing rules send TCP to. The host key fingerprint is required — an exit
that trusts whatever answers first is not an exit an operator can vouch for.
The fetch button asks the panel to scan the server's key, so the operator
confirms a pin instead of typing one.
*/

function required(key: string) {
  return { validate: (value: unknown) => (value ? true : key) };
}

export default function SshExitFields() {
  const { t } = useTranslation();
  const { getValues, setValue } = useFormContext();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [fetching, setFetching] = useState(false);

  async function fetchFingerprint() {
    const host = String(getValues('sshHost') ?? '').trim();
    const port = Number(getValues('sshPort') ?? 22);
    if (!host) {
      messageApi.warning(t('pages.xray.egress.sshHostRequired'));
      return;
    }
    setFetching(true);
    try {
      const raw = await HttpUtil.post('/panel/api/egresses/hostkey', { host, port }, {
        headers: { 'Content-Type': 'application/json' },
      });
      const msg = parseMsg(raw, SSHHostKeySchema, 'egresses/hostkey');
      if (!msg?.success || !msg.obj?.fingerprint) {
        messageApi.error(msg?.msg || t('pages.xray.egress.sshFingerprintFetchFailed'));
        return;
      }
      setValue('sshFingerprint', msg.obj.fingerprint, { shouldDirty: true, shouldValidate: true });
      messageApi.info(`${msg.obj.keyType}: ${t('pages.xray.egress.sshFingerprintFetched')}`);
    } catch (e) {
      messageApi.error(`${t('pages.xray.egress.sshFingerprintFetchFailed')}: ${String(e)}`);
    } finally {
      setFetching(false);
    }
  }

  return (
    <>
      {messageContextHolder}
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
        extra={(
          <Button type="link" size="small" style={{ padding: 0 }} loading={fetching} onClick={fetchFingerprint}>
            {t('pages.xray.egress.sshFingerprintFetch')}
          </Button>
        )}
      >
        <Input id="sshFingerprint" placeholder="SHA256:…" autoComplete="off" />
      </FormField>
    </>
  );
}
