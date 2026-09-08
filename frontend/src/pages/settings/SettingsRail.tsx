import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from 'antd';

import { Icon, type IconName } from '@/components/ui/Icon';

export type SettingsSection = { slug: string; labelKey: string; icon: IconName };

/*
The settings categories are chosen by the URL hash. They used to be reachable
only through a submenu on the old sidebar, so when the shell replaced it they
became unreachable without typing the fragment by hand. This rail is where they
live now — and unlike a submenu it can be searched.
*/
export default function SettingsRail({ sections, active, onPick }: {
  sections: SettingsSection[];
  active: string;
  onPick: (slug: string) => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState('');

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return sections;
    return sections.filter((s) => {
      const desc = t(`pages.settings.sectionDesc_${s.slug.replace(/-/g, '_')}`);
      return `${t(s.labelKey)} ${desc}`.toLowerCase().includes(needle);
    });
  }, [sections, q, t]);

  return (
    <aside className="p-card p-settings-rail">
      <div style={{ padding: 10, borderBottom: '1px solid var(--p-border)' }}>
        <Input
          size="small"
          allowClear
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('pages.settings.findSetting')}
          aria-label={t('pages.settings.findSetting')}
          prefix={<Icon name="search" size={14} style={{ color: 'var(--p-text-2)' }} />}
        />
      </div>
      <div style={{ padding: 8, display: 'flex', flexDirection: 'column', gap: 1 }}>
        {shown.map((s) => (
          <button
            type="button"
            key={s.slug}
            className={`p-navitem${active === s.slug ? ' is-active' : ''}`}
            style={{ height: 'auto', padding: '8px 10px', alignItems: 'flex-start' }}
            aria-current={active === s.slug ? 'page' : undefined}
            onClick={() => onPick(s.slug)}
          >
            <span className="p-navicon" style={{ marginTop: 2 }}><Icon name={s.icon} size={15} /></span>
            <span style={{ minWidth: 0 }}>
              <span style={{ display: 'block' }}>{t(s.labelKey)}</span>
              <span className="p-sub" style={{ display: 'block', whiteSpace: 'normal', marginTop: 1 }}>
                {t(`pages.settings.sectionDesc_${s.slug.replace(/-/g, '_')}`)}
              </span>
            </span>
          </button>
        ))}
        {!shown.length && <div className="p-empty" style={{ padding: 20 }}>{t('pages.settings.noSettingMatch', { q })}</div>}
      </div>
    </aside>
  );
}
