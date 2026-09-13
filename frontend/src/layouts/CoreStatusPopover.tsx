import { useTranslation } from 'react-i18next';
import { Button, Popover } from 'antd';
import type { CoreStatusView } from '@/generated/zod';

import { Icon, type IconName } from '@/components/ui/Icon';
import { StatusDot } from '@/components/ui/panel';
import { useCoresStatusQuery } from '@/api/queries/useCoresStatusQuery';
import { coreRuntime, summarizeCores } from '@/lib/cores/status';
import { coreStateTone, coreVersionContextKey, coreVersionLabel, orderCores } from '@/lib/cores/display';

function isHealthy(core: CoreStatusView): boolean {
  return coreRuntime(core).healthy;
}

function toneOf(core: CoreStatusView): string {
  const colors = { success: 'var(--p-success)', warning: 'var(--p-warning)', error: 'var(--p-error)', default: '#8b8d94' };
  return colors[coreStateTone(core).tone];
}

function iconOf(core: CoreStatusView): IconName {
  if (isHealthy(core)) return 'check';
  if (coreRuntime(core).state === 'stopped') return 'power';
  return 'gauge';
}

/* `since` is a millisecond epoch, the same value the Cores page formats. */
function uptimeOf(core: CoreStatusView, since?: number): string {
  if (core.state !== 'running' || !since) return '';
  const minutes = Math.floor((Date.now() - since) / 60_000);
  if (minutes < 60) return `${Math.max(minutes, 0)}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

function CorePanel({ cores, onManage, onRefresh }: {
  cores: CoreStatusView[];
  onManage: () => void;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const summary = summarizeCores(cores);
  const running = summary.running;
  return (
    <div className="p-pop">
      <div className="p-pop-h">
        <div>
          <div style={{ fontSize: 13, fontWeight: 650, color: 'var(--p-text)' }}>{t('menu.cores')}</div>
          <div className="p-sub" style={{ marginTop: 1 }}>{t('menu.coresRunning', { running, total: cores.length })}</div>
          <div className="p-sub">{t('coreRuntime.healthSummary', { healthy: summary.healthy, unverified: summary.running - summary.healthy })}</div>
        </div>
        <Button size="small" icon={<Icon name="refresh" size={14} />} onClick={onRefresh} aria-label={t('menu.refresh')} />
      </div>
      <div className="p-pop-b">
        {orderCores(cores).map((core) => {
          const tone = toneOf(core);
          const uptime = uptimeOf(core, core.since);
          return (
            <button type="button" key={core.id} className="p-poprow" onClick={onManage} title={t(coreRuntime(core).descriptionKey)}>
              <span className="p-alert-i" style={{ background: `color-mix(in srgb, ${tone} 14%, transparent)`, color: tone }}>
                <Icon name={iconOf(core)} size={13} />
              </span>
              <span style={{ flex: 1, minWidth: 0 }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                  <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--p-text)' }}>{t(core.titleKey)}</span>
                </span>
                <span className="p-sub" style={{ display: 'block', marginTop: 1 }}>
                  <span className="p-mono" style={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                    title={`${coreVersionLabel(core).text ?? t(coreVersionLabel(core).key!)} · ${t(coreVersionContextKey(core))}`}>{coreVersionLabel(core).text ?? t(coreVersionLabel(core).key!)}</span>
                  {` · ${t(coreStateTone(core).labelKey)}`}{uptime ? ` · ${uptime}` : ''}
                </span>
              </span>
              <span className="p-poprow-x"><Icon name="chevron" size={15} /></span>
            </button>
          );
        })}
        {!cores.length && <div className="p-empty" style={{ padding: 26 }}>{t('menu.nothingHere')}</div>}
      </div>
      <div className="p-pop-f">
        <span className="p-sub">{t('menu.coresRunning', { running, total: cores.length })}</span>
        <Button size="small" type="primary" onClick={onManage}>{t('menu.manageCores')}</Button>
      </div>
    </div>
  );
}

export default function CoreStatusPopover({ onManage }: { onManage: () => void }) {
  const { t } = useTranslation();
  const { cores, refetch } = useCoresStatusQuery();
  const summary = summarizeCores(cores);
  const running = summary.running;
  const allOk = cores.length > 0 && summary.healthy === cores.length;
  return (
    <Popover
      content={<CorePanel cores={cores} onManage={onManage} onRefresh={() => void refetch()} />}
      trigger="click"
      placement="bottomRight"
      arrow={false}
    >
      <button type="button" className="p-corestatus" aria-label={t('menu.cores')}>
        <StatusDot color={allOk ? 'var(--p-success)' : 'var(--p-warning)'} pulse={allOk} />
        <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--p-text)' }}>
          {t('menu.coresBadge', { running, total: cores.length })}
        </span>
        <Icon name="chevronDown" size={13} />
      </button>
    </Popover>
  );
}
