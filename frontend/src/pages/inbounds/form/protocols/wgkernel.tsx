import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, InputNumber, Select, Space, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import { FormField } from '@/components/form/rhf';
import type { WireguardPoolUsage } from '@/lib/xray/wireguard-pool';
import { normalizeInterfaceAddresses } from '@/lib/xray/interface-address';

interface WgkernelFieldsProps {
  wgPubKey: string;
  regenInboundWg: () => void;
  /* Backend sentences naming a sysctl this host has off, rendered and never
     translated. Empty means the host forwards, or that it could not be asked. */
  forwardingNotes: string[];
  /* How full the configured prefix is, or null when there is no IPv4 pool to size. */
  poolUsage: WireguardPoolUsage | null;
}

/* Kernel WireGuard has no streamSettings and no TUN emulation, so this form is
   the device itself: its keypair, the address it answers on, MTU and DNS. */
export default function WgkernelFields({
  wgPubKey,
  regenInboundWg,
  forwardingNotes,
  poolUsage,
}: WgkernelFieldsProps) {
  const { t } = useTranslation();
  const [rejected, setRejected] = useState<{ reason: string; values: string[] } | null>(null);
  return (
    <>
      <Alert
        style={{ marginBottom: 16 }}
        type={forwardingNotes.length > 0 ? 'error' : 'warning'}
        showIcon
        title={t('pages.inbounds.form.wgkernelForwardingTitle')}
        description={(
          <>
            {forwardingNotes.length > 0 && (
              <div style={{ marginBottom: 6, fontWeight: 600 }}>
                {t('pages.inbounds.form.wgkernelForwardingOff')}
                {forwardingNotes.map((note) => (
                  <div key={note} style={{ fontWeight: 400 }} dir="ltr">{note}</div>
                ))}
              </div>
            )}
            <div>{t('pages.inbounds.form.wgkernelForwardingHint')}</div>
            <div><code>printf &apos;net.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=1\n&apos; &gt; /etc/sysctl.d/99-wgkernel.conf</code></div>
            <div><code>sysctl --system</code></div>
            <div><code>nft add table ip pui_nat</code></div>
            <div><code>nft &apos;add chain ip pui_nat postrouting {'{'} type nat hook postrouting priority srcnat; policy accept; {'}'}&apos;</code></div>
            <div><code>nft add rule ip pui_nat postrouting iifname &quot;pwg*&quot; oifname != &quot;pwg*&quot; masquerade</code></div>
          </>
        )}
      />
      <Form.Item label={t('pages.xray.wireguard.secretKey')}>
        <Space.Compact block>
          <FormField name={['settings', 'secretKey']} noStyle>
            <Input style={{ width: 'calc(100% - 32px)' }} />
          </FormField>
          <Button aria-label={t('regenerate')} icon={<ReloadOutlined />} onClick={regenInboundWg} />
        </Space.Compact>
      </Form.Item>
      <Form.Item label={t('pages.xray.wireguard.publicKey')}>
        <Input value={wgPubKey} disabled />
      </Form.Item>
      <FormField
        name={['settings', 'address']}
        label={t('pages.inbounds.form.wgkernelAddress')}
        transform={{
          output: (raw) => {
            const result = normalizeInterfaceAddresses((raw as string[]) ?? []);
            setRejected(result.rejected.length > 0 && result.reason
              ? { reason: result.reason, values: result.rejected }
              : null);
            return result.values;
          },
        }}
        extra={(
          <>
            <div>{t('pages.inbounds.form.wgkernelAddressHint')}</div>
            {rejected && (
              <Typography.Text type="danger">
                {t(rejected.reason, { value: rejected.values.join(', ') })}
              </Typography.Text>
            )}
            {poolUsage && (
              <div>
                <Typography.Text type={poolUsage.outside > 0 ? 'warning' : undefined}>
                  {t('pages.inbounds.form.wgkernelPoolUsage', {
                    used: poolUsage.used, capacity: poolUsage.capacity, prefix: poolUsage.prefix,
                  })}
                </Typography.Text>
                {poolUsage.outside > 0 && (
                  <div>{t('pages.inbounds.form.wgkernelPoolOutside', { n: poolUsage.outside })}</div>
                )}
              </div>
            )}
            <div>{t('pages.inbounds.form.wgkernelPoolSizing')}</div>
          </>
        )}
      >
        <Select id="wgkernelAddress" mode="tags" tokenSeparators={[',']} style={{ width: '100%' }} placeholder="10.0.0.1/24" />
      </FormField>
      <FormField name={['settings', 'mtu']} label="MTU">
        <InputNumber />
      </FormField>
      <FormField name={['settings', 'dns']} label={t('pages.inbounds.info.dns')}>
        <Input placeholder="1.1.1.1, 1.0.0.1" />
      </FormField>
    </>
  );
}
