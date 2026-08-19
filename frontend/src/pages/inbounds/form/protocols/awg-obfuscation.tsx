import { useTranslation } from 'react-i18next';
import { Alert, Button, Collapse, Input, InputNumber, Space, Switch, Typography } from 'antd';
import { useFormContext } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';

/* One preset, because every value here has to match on the client too, and
   picking a dozen numbers by hand is how a deployment ends up with a tunnel
   that handshakes and carries nothing. */
const PRESET: Record<string, number> = {
  'settings.awg.jc': 4,
  'settings.awg.jmin': 40,
  'settings.awg.jmax': 70,
  'settings.awg.s1': 20,
  'settings.awg.s2': 30,
};

/*
AmneziaWG's obfuscation, a section rather than its own tab because an inbound
with none of it set is plain WireGuard carried by the AmneziaWG module — which
works, and is a legitimate thing to run.

Every field belongs to a CLIENT's configuration rather than being a server-side
knob: the panel writes each one into the .conf it hands out, and a client whose
values differ cannot recognise the server's packets at all.
*/
export default function AwgObfuscationFields() {
  const { t } = useTranslation();
  const { setValue } = useFormContext();

  const applyPreset = () => {
    for (const path of Object.keys(PRESET)) {
      setValue(path, PRESET[path], { shouldDirty: true });
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
              name="settings.awg.jc"
              tooltip={t('pages.inbounds.form.awgJcHint')}
            >
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgJmin')} name="settings.awg.jmin">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgJmax')} name="settings.awg.jmax">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>

            {/* Literal keys rather than built from the field name: the dead-key
                guard scans sources statically and cannot see a computed one. */}
            <FormField label={t('pages.inbounds.form.awgS1')} name="settings.awg.s1">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS2')} name="settings.awg.s2">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS3')} name="settings.awg.s3">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgS4')} name="settings.awg.s4">
              <InputNumber min={0} max={65535} style={{ width: '100%' }} />
            </FormField>

            {/* The rest of 3.1, behind their own heading: a deployment that sets
                junk and padding is already obfuscated, and these are for one that
                needs to look different again. Ranges are written lo-hi. */}
            <Typography.Text type="secondary">{t('pages.inbounds.form.awgAdvanced')}</Typography.Text>

            <FormField label={t('pages.inbounds.form.awgI1')} name="settings.awg.i1" tooltip={t('pages.inbounds.form.awgIHint')}>
              <Input placeholder="b0xdeadbeef" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI2')} name="settings.awg.i2">
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI3')} name="settings.awg.i3">
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI4')} name="settings.awg.i4">
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgI5')} name="settings.awg.i5">
              <Input />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgHeaderProtectionKey')} name="settings.awg.headerProtectionKey" tooltip={t('pages.inbounds.form.awgHeaderProtectionKeyHint')}>
              <Input placeholder="base64, 32 bytes" />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgRandomTrailers')} name="settings.awg.randomTrailers" valueProp="checked">
              <Switch />
            </FormField>
            <FormField label={t('pages.inbounds.form.awgDisableCookies')} name="settings.awg.disableCookies" valueProp="checked">
              <Switch />
            </FormField>
          </Space>
        ),
      }]}
    />
  );
}
