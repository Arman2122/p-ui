import { useTranslation } from 'react-i18next';
import { Button, Tooltip } from 'antd';
import type { CoreStatusView } from '@/generated/zod';

import { Icon } from '@/components/ui/Icon';
import { StatusDot } from '@/components/ui/panel';
import { summarizeCores } from '@/lib/cores/status';
import { coreIcon, coreColor, coreStateTone, coreVersionLabel, orderCores } from '@/lib/cores/display';
import { TimeFormatter } from '@/utils';

const TONE_COLOR: Record<string, string> = {
  success: 'var(--p-success)',
  warning: 'var(--p-warning)',
  error: 'var(--p-error)',
  default: '#8b8d94',
};

/* Only Xray reports a top-level start time; a per-inbound core carries one per
   instance, and the kernel modules report none at all. */
export function coreSince(core: CoreStatusView): number | undefined {
  if (core.since) return core.since;
  const running = (core.instances ?? [])
    .filter((i) => i.state === 'running' && i.since)
    .map((i) => i.since as number);
  return running.length ? Math.min(...running) : undefined;
}

function uptimeOf(core: CoreStatusView): string {
  const since = coreSince(core);
  if (!since) return '';
  return TimeFormatter.formatSecond(Math.max(0, (Date.now() - since) / 1000));
}

export function CoreSummaryStrip({ cores, onOpen }: { cores: CoreStatusView[]; onOpen: () => void }) {
  const { t } = useTranslation();
  const summary = summarizeCores(cores);
  const stopped = cores.filter((c) => !c.available || c.state !== 'running' || !c.ready);
  return (
    <div className="p-vitals-core">
      <div className="ov-core-summary-heading">
        <div style={{ minWidth: 0 }}>
          <div className="ov-core-summary-count">
            {t('pages.index.coresRunning', { running: summary.healthy, total: summary.total })}
          </div>
          <div className="p-sub ov-core-summary-state" title={stopped.map((c) => `${t(c.titleKey)} · ${t(coreStateTone(c).labelKey)}`).join(' · ')}>
            {stopped.length
              ? stopped.map((c) => `${t(c.titleKey)} · ${t(coreStateTone(c).labelKey)}`).join(' · ')
              : cores.length ? t('pages.index.coresAllAnswering') : t('telemetry.noReadings')}
          </div>
        </div>
        <Button size="small" type="text" onClick={onOpen} aria-label={t('menu.manageCores')} icon={<Icon name="chevron" size={14} />} />
      </div>
      <div className="ov-core-summary-chips">
        {orderCores(cores).map((c) => {
          const tone = coreStateTone(c);
          return (
            <Tooltip key={c.id} title={`${t(c.titleKey)} · ${t(tone.labelKey)}`}>
              <button type="button" className="p-corechip" onClick={onOpen}>
                <StatusDot color={TONE_COLOR[tone.tone]} pulse={tone.tone === 'success'} />
                {t(c.titleKey)}
              </button>
            </Tooltip>
          );
        })}
      </div>
    </div>
  );
}

export default function DashboardCores({ cores, onOpen }: { cores: CoreStatusView[]; onOpen: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="p-coregrid">
      {orderCores(cores).map((c) => {
        const tone = coreStateTone(c);
        const uptime = c.available && c.state === 'running' && c.ready ? uptimeOf(c) : '';
        return (
          <button type="button" key={c.id} className="p-coretile" onClick={onOpen}>
            <span className="p-coretile-h">
              <span
                className="p-alert-i"
                style={{ background: `color-mix(in srgb, ${coreColor(c.id)} 15%, transparent)`, color: coreColor(c.id), width: 24, height: 24, flex: '0 0 24px' }}
              >
                <Icon name={coreIcon(c.id)} size={13} />
              </span>
              <span className="p-coretile-n">{t(c.titleKey)}</span>
              <StatusDot color={TONE_COLOR[tone.tone]} pulse={tone.tone === 'success'} />
            </span>
            <span className="p-coretile-m">
              <span className="p-mono">{coreVersionLabel(c).text ?? t(coreVersionLabel(c).key!)}</span>
              <span>{uptime || t(tone.labelKey)}</span>
            </span>
          </button>
        );
      })}
    </div>
  );
}
