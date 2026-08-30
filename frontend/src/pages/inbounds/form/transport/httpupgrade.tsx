import { useTranslation } from 'react-i18next';
import { Input, Switch } from 'antd';

import { HeaderMapEditor } from '@/components/form';
import { FormField } from '@/components/form/rhf';
import { headerMapNamesHost } from '@/lib/xray/transport-limits';

export default function HttpUpgradeForm() {
  const { t } = useTranslation();
  return (
    <>
      <FormField
        name={['streamSettings', 'httpupgradeSettings', 'acceptProxyProtocol']}
        label={t('pages.inbounds.form.proxyProtocol')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      <FormField
        name={['streamSettings', 'httpupgradeSettings', 'host']}
        label={t('host')}
      >
        <Input />
      </FormField>
      <FormField
        name={['streamSettings', 'httpupgradeSettings', 'path']}
        label={t('path')}
      >
        <Input />
      </FormField>
      <FormField
        label={t('pages.inbounds.form.headers')}
        name={['streamSettings', 'httpupgradeSettings', 'headers']}
        rules={{
          validate: (value: unknown) =>
            !headerMapNamesHost(value) || 'pages.inbounds.form.headerHostNotAllowed',
        }}
      >
        <HeaderMapEditor mode="v1" />
      </FormField>
    </>
  );
}
