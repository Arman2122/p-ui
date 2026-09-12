import { useId, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Input } from 'antd';

import { Icon, type IconName } from '@/components/ui/Icon';
import { useMediaQuery } from '@/hooks/useMediaQuery';

export interface SectionRailItem {
  key: string;
  label: string;
  /** One line saying what the section covers. Also what the search matches on. */
  desc?: string;
  icon: IconName;
  extra?: ReactNode;
}

export default function SectionRail({ items, active, onPick, searchable = true, mobileCollapsible = false }: {
  items: SectionRailItem[];
  active: string;
  onPick: (key: string) => void;
  searchable?: boolean;
  mobileCollapsible?: boolean;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const [expanded, setExpanded] = useState(false);
  const { isMobile } = useMediaQuery(900);
  const controlsId = useId();
  const compact = mobileCollapsible && isMobile;

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return items;
    return items.filter((s) => `${s.label} ${s.desc ?? ''}`.toLowerCase().includes(needle));
  }, [items, q]);

  return (
    <aside className="p-card p-settings-rail">
      {compact && <button type="button" className="settings-rail-toggle" aria-expanded={expanded} aria-controls={controlsId} onClick={() => setExpanded((value) => !value)}>
        <span>{t('uiRepair.settings.sections')}</span>
        <strong>{items.find((item) => item.key === active)?.label}</strong>
        <span aria-hidden="true">{expanded ? '−' : '+'}</span>
      </button>}
      <div id={controlsId} hidden={compact && !expanded}>
        {searchable && (
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
        )}
        <div className="p-section-items" style={{ padding: 8, display: 'flex', flexDirection: 'column', gap: 1 }}>
          {shown.map((s) => (
            <button
              type="button"
              key={s.key}
              className={`p-navitem${active === s.key ? ' is-active' : ''}`}
              style={{ height: 'auto', padding: '8px 10px', alignItems: 'flex-start', textAlign: 'start' }}
              aria-current={active === s.key ? 'page' : undefined}
              onClick={() => { onPick(s.key); if (compact) setExpanded(false); }}
            >
              <span className="p-navicon" style={{ marginTop: 2 }}><Icon name={s.icon} size={15} /></span>
              <span style={{ minWidth: 0, flex: 1 }}>
                <span style={{ display: 'block' }}>{s.label}</span>
                {s.desc && (
                  <span className="p-sub" style={{ display: 'block', whiteSpace: 'normal', marginTop: 1 }}>{s.desc}</span>
                )}
              </span>
              {s.extra}
            </button>
          ))}
          {!shown.length && <div className="p-empty" style={{ padding: 20 }}>{t('pages.settings.noSettingMatch', { q })}</div>}
        </div>
      </div>
    </aside>
  );
}
