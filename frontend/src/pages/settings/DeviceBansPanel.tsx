import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input, InputNumber, Space } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

import SettingsGroup from './SettingsGroup';
import { onNumber } from '@/utils/onNumber';
import { BansTable } from '@/components/clients/BansTable';
import { useBanMutations, useBansQuery } from '@/api/queries/useBansQuery';

/* The operator's view of the device cap's bans: what is dropped where and until
   when, a release for each, and a ban by hand for an address they name. */
export default function DeviceBansPanel({ datepicker }: { datepicker?: 'gregorian' | 'jalalian' }) {
  const { t } = useTranslation();
  const { bans, loading, refetch } = useBansQuery();
  const { release, releaseAll, ban, saving } = useBanMutations();
  const [source, setSource] = useState('');
  const [minutes, setMinutes] = useState(0);

  const banNow = () => {
    const address = source.trim();
    if (!address) return;
    void ban(address, minutes).then((msg) => { if (msg?.success) setSource(''); });
  };

  return (
    <SettingsGroup title={t('pages.settings.ipLimitBans')} description={t('pages.settings.ipLimitBansDesc')}>
      <div className="settings-editor settings-bans">
        <Space.Compact style={{ width: '100%', marginBottom: 8 }}>
          <Input
            value={source}
            placeholder="203.0.113.7"
            aria-label={t('pages.settings.ipLimitBanSource')}
            onChange={(e) => setSource(e.target.value)}
            onPressEnter={banNow}
          />
          <InputNumber
            value={minutes}
            min={0}
            aria-label={t('pages.settings.ipLimitBanMinutes')}
            onChange={onNumber((v) => setMinutes(v))}
          />
          <Button onClick={banNow} disabled={saving || !source.trim()}>{t('pages.settings.ipLimitBanAddress')}</Button>
          <Button icon={<ReloadOutlined />} aria-label={t('refresh')} loading={loading} onClick={() => { void refetch(); }} />
        </Space.Compact>
        <BansTable
          bans={bans}
          loading={loading}
          releasing={saving}
          datepicker={datepicker}
          onRelease={(email, address) => { void release(email, address); }}
          onReleaseAll={() => { void releaseAll(); }}
        />
      </div>
    </SettingsGroup>
  );
}
