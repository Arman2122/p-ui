import { useTranslation } from 'react-i18next';
import { Alert, Button, Form, Input, InputNumber, Select, Space } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import { FormField } from '@/components/form/rhf';

interface WgkernelFieldsProps {
  wgPubKey: string;
  regenInboundWg: () => void;
}

/* Kernel WireGuard has no streamSettings and no TUN emulation, so this form is
   the device itself: its keypair, the address it answers on, MTU and DNS. */
export default function WgkernelFields({ wgPubKey, regenInboundWg }: WgkernelFieldsProps) {
  const { t } = useTranslation();
  return (
    <>
      <Alert
        style={{ marginBottom: 16 }}
        type="warning"
        showIcon
        title={t('pages.inbounds.form.wgkernelForwardingTitle')}
        description={(
          <>
            <div>{t('pages.inbounds.form.wgkernelForwardingHint')}</div>
            <div><code>sysctl -w net.ipv4.ip_forward=1</code></div>
            <div><code>iptables -t nat -A POSTROUTING -s 10.0.0.0/24 -j MASQUERADE</code></div>
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
        extra={t('pages.inbounds.form.wgkernelAddressHint')}
      >
        <Select mode="tags" tokenSeparators={[',']} style={{ width: '100%' }} placeholder="10.0.0.1/24" />
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
