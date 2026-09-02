import { useTranslation } from 'react-i18next';
import { Alert, Button, Collapse, Input, InputNumber, Space, Switch, Typography } from 'antd';
import { useFormContext } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';

/* One preset, because every value here has to match on the client too, and
   picking a dozen numbers by hand is how a deployment ends up with a tunnel
   that handshakes and carries nothing. */
const PRESET: Record<string, number> = {
  jc: 4,
  jmin: 40,
  jmax: 70,
  s1: 20,
  s2: 30,
};

interface AwgObfuscationFieldsProps {
  /* Where the params sit in this form. An inbound nests them under its settings;
     an exit row carries them beside the uplink's own flat fields. */
  prefix?: string;
}

/*
AmneziaWG's obfuscation, a section rather than its own tab because a device with
none of it set is plain WireGuard carried by the AmneziaWG module — which works,
and is a legitimate thing to run.

Every field belongs to a PEER's configuration rather than being a local knob:
both ends write the same values, and a peer whose values differ cannot recognise
the other's packets at all.
*/
export default function AwgObfuscationFields({ prefix = 'settings.awg' }: AwgObfuscationFieldsProps) {
  const { t } = useTranslation();
  const { setValue } = useFormContext();
  const at = (field: string) => `${prefix}.${field}`;

  const applyPreset = () => {
    for (const [field, value] of Object.entries(PRESET)) {
      setValue(at(field), value, { shouldDirty: true });
    }
  };

  return (
    <Collapse
      items={[{
        key: 'awg',
        label: t('pages.inbounds.form.awgObfuscation'),
        children: (
          <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
            <Alert
              type="info"
              showIcon
              description={t('pages.inbounds.form.awgObfuscationHint')}
              action={<Button size="small" onClick={applyPreset}>{t('pages.inbounds.form.awgPreset')}</Button>}
            />

            <FormField
              label={t('pages.inbounds.form.awgJc')}
              name={at('jc')}
              tooltip={t('pages.inbounds.form.awgJcHint')}
            >
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgJmin')} name={at('jmin')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgJmax')} name={at('jmax')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>

            {/* Only the field NAME is computed. Every t() key stays a literal,
                because the dead-key guard scans sources statically. */}
            <FormField label={t('pages.inbounds.form.awgS1')} name={at('s1')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS2')} name={at('s2')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS3')} name={at('s3')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS4')} name={at('s4')}>
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>

            {/* The rest of 3.1, behind their own heading: a deployment that sets
                junk and padding is already obfuscated, and these are for one that
                needs to look different again. Ranges are written lo-hi. */}
            <Typography.Text type="secondary">{t('pages.inbounds.form.awgAdvanced')}</Typography.Text>

            <FormField label={t('pages.inbounds.form.awgI1')} name={at('i1')} tooltip={t('pages.inbounds.form.awgIHint')}>
              <Input placeholder="b0xdeadbeef" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI2')} name={at('i2')}>
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI3')} name={at('i3')}>
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI4')} name={at('i4')}>
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI5')} name={at('i5')}>
              <Input />
            </FormField>
            <Typography.Text type="secondary">{t('pages.inbounds.form.awgRanges')}</Typography.Text>
            <FormField label={t('pages.inbounds.form.awgH1')} name={at('h1')} tooltip={t('pages.inbounds.form.awgHHint')}>
              <Input placeholder="10-19" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgH2')} name={at('h2')}>
              <Input placeholder="20-29" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgH3')} name={at('h3')}>
              <Input placeholder="30-39" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgH4')} name={at('h4')}>
              <Input placeholder="40-49" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgContentPaddingAddition')} name={at('contentPaddingAddition')} tooltip={t('pages.inbounds.form.awgRangeHint')}>
              <Input placeholder="0-64" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgRekeyAfterTime')} name={at('rekeyAfterTime')} tooltip={t('pages.inbounds.form.awgRangeHint')}>
              <Input placeholder="120-180" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgRekeyTimeout')} name={at('rekeyTimeout')}>
              <Input placeholder="5-10" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgRejectAfterTime')} name={at('rejectAfterTime')}>
              <Input placeholder="180-240" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgKeepaliveTimeout')} name={at('keepaliveTimeout')}>
              <Input placeholder="10-15" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgMaxHandshakeAttempts')} name={at('maxHandshakeAttempts')}>
              <Input placeholder="10-20" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgHeaderProtectionKey')} name={at('headerProtectionKey')} tooltip={t('pages.inbounds.form.awgHeaderProtectionKeyHint')}>
              <Input placeholder="base64, 32 bytes" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgRandomTrailers')} name={at('randomTrailers')} valueProp="checked">
              <Switch />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgDisableCookies')} name={at('disableCookies')} valueProp="checked">
              <Switch />
            </FormField>
          </Space>
        ),
      }]}
    />
  );
}
