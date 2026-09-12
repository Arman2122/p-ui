import { Button, Dropdown, Tag, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';
import type { CoreStatusView, CoreView } from '@/generated/zod';
import { Icon, type IconName } from '@/components/ui/Icon';
import { Field, Kicker, PanelCard, StatusChip } from '@/components/ui/panel';
import { coreColor, coreIcon, coreMaintenanceKey, coreRestarts, coreStateTone, coreVersionLabel } from '@/lib/cores/display';
import { SizeFormatter, TimeFormatter } from '@/utils';
import { coreUptime, type CoreMetrics } from './useCoreInventory';
import LiveTrafficTrend from '@/components/viz/LiveTrafficTrend';
import type { LiveTrafficSeries } from '@/hooks/useLiveTrafficHistory';

export type CoreTab = 'status' | 'runtime' | 'logging' | 'inbounds' | 'version';
export const coreBytes = (value: number | undefined) => value === undefined ? '—' : SizeFormatter.sizeFormat(value).replace(/(\.\d)\d (?=[KMGTPE]?B)/, '$1 ');

export function CoreStatus({ core }: { core: CoreStatusView }) {
  const { t } = useTranslation();
  const tone = coreStateTone(core);
  const colors = { success: 'var(--p-success)', warning: 'var(--p-warning)', error: 'var(--p-error)', default: '#8b8d94' };
  return <Tooltip title={core.unavailable || core.healthError || core.detail}><span><StatusChip status={{ key: core.state, label: t(tone.labelKey), color: colors[tone.tone] }} /></span></Tooltip>;
}

export function CoreTrafficSlot({ height = 26, color, series }: { height?: number; color?: string; series?: LiveTrafficSeries }) {
  return <div className="core-traffic-slot" style={{ height }}><LiveTrafficTrend series={series} height={height} color={color} /></div>;
}

interface CorePresentationProps {
  core: CoreStatusView; descriptor?: CoreView; metrics: CoreMetrics; update?: string | null;
  series?: LiveTrafficSeries;
  restarting: boolean; onRestart: () => void; onOpen: (tab: CoreTab) => void;
}

function CoreActions({ core, restarting, onRestart, onOpen, card = false }: CorePresentationProps & { card?: boolean }) {
  const { t } = useTranslation();
  const action = (icon: IconName, title: string, onClick: () => void, disabled = false, loading = false) =>
    <Tooltip title={disabled ? core.unavailable || title : title}><Button type={card ? 'default' : 'text'} size="small" aria-label={title} disabled={disabled} loading={loading}
      icon={<Icon name={icon} size={15} />} onClick={onClick}>{card ? title : null}</Button></Tooltip>;
  return <div className={card ? 'core-card-actions' : 'p-acts core-row-actions'} role="group" aria-label={t('pages.cores.columns.actions')}>
    {action('restart', t('pages.cores.restart'), onRestart, !core.available, restarting)}
    {card ? action('logs', t('pages.cores.logs'), () => onOpen('logging'), !core.logs)
      : action('inbounds', t('menu.inbounds'), () => onOpen('inbounds'))}
    {!card && action('config', t('pages.cores.fidelity.config'), () => onOpen('runtime'))}
    <Tooltip title={t('pages.cores.fidelity.more')}><Dropdown trigger={['click']} menu={{ items: [
      { key: 'status', label: t('pages.cores.columns.state'), icon: <Icon name="gauge" size={14} /> },
      { key: 'logging', label: t('pages.cores.logs'), icon: <Icon name="logs" size={14} />, disabled: !core.logs },
      { key: 'runtime', label: t('pages.cores.fidelity.config'), icon: <Icon name="config" size={14} /> },
      { key: 'inbounds', label: t('menu.inbounds'), icon: <Icon name="inbounds" size={14} /> },
      { key: 'version', label: t('pages.cores.versions'), icon: <Icon name="up" size={14} /> },
    ], onClick: ({ key }) => onOpen(key as CoreTab) }}>
      <Button type="text" size="small" aria-label={t('pages.cores.fidelity.more')} icon={<Icon name="more" size={15} />} />
    </Dropdown></Tooltip>
  </div>;
}

export function CoreRow(props: CorePresentationProps) {
  const { core, metrics, update, onOpen } = props;
  const { t } = useTranslation();
  const version = coreVersionLabel(core);
  const uptime = coreUptime(core);
  return <div className="p-corerow" data-core-id={core.id}>
    <span className="p-alert-i" style={{ color: coreColor(core.id), background: `color-mix(in srgb, ${coreColor(core.id)} 15%, transparent)` }}><Icon name={coreIcon(core.id)} size={14} /></span>
    <div className="p-corerow-id">
      <div className="core-row-title"><button className="p-text-action p-name" type="button" onClick={() => onOpen('status')}>{t(core.titleKey)}</button><CoreStatus core={core} />
        {update && <Tag color="orange">{update}</Tag>}</div>
      <div className="p-sub core-version">{core.id} · <Tooltip title={<>{t(coreMaintenanceKey(core))}{core.version.error && <div>{core.version.error}</div>}</>}><span className="p-mono">{version.text ?? t(version.key!)}</span></Tooltip>
        {uptime !== undefined && <> · {t('pages.cores.fidelity.upFor', { duration: TimeFormatter.formatSecond(uptime) })}</>}</div>
      {(core.unavailable || core.healthError) && <Tooltip title={core.unavailable || core.healthError}><div className="p-sub core-unavailable">{core.unavailable || core.healthError}</div></Tooltip>}
    </div>
    <div className="p-corerow-stats">
      <Field label={t('menu.inbounds')} value={metrics.inbounds?.length ?? '—'} />
      <Field label={t('menu.clients')} value={<>{metrics.clients ?? '—'} · <Tooltip title={t('pages.cores.fidelity.liveUnavailable')}><span>{t('pages.cores.fidelity.liveCount', { value: '—' })}</span></Tooltip></>} />
      <Field label={t('pages.cores.fidelity.traffic')} value={coreBytes(metrics.traffic)} />
      <CoreTrafficSlot series={props.series} color={coreColor(core.id)} />
    </div>
    <CoreActions {...props} />
  </div>;
}

export function CoreCard(props: CorePresentationProps) {
  const { core, descriptor, metrics, update, onOpen } = props;
  const { t } = useTranslation();
  const version = coreVersionLabel(core);
  const uptime = coreUptime(core);
  return <article data-core-id={core.id}><PanelCard pad={0} className="core-card" icon={coreIcon(core.id)}
    title={<button type="button" className="p-text-action" onClick={() => onOpen('status')}>{t(core.titleKey)}</button>}
    extra={<>{update && <Tag color="orange">{update}</Tag>}<CoreStatus core={core} /></>}>
    <div className="core-card-version"><div><Tooltip title={t(coreMaintenanceKey(core))}><span className="p-mono">{version.text ?? t(version.key!)}</span></Tooltip><div className="p-sub">{core.id}</div></div>
      <Field label={t('pages.cores.fidelity.uptime')} value={uptime === undefined ? '—' : TimeFormatter.formatSecond(uptime)} align="end" /></div>
    {core.unavailable && <div className="p-sub core-card-reason">{core.unavailable}</div>}
    <CoreTrafficSlot height={50} series={props.series} color={coreColor(core.id)} />
    <div className="core-card-stats">{[[t('menu.inbounds'), metrics.inbounds?.length ?? '—'], [t('menu.clients'), metrics.clients ?? '—'], [t('pages.cores.fidelity.traffic'), coreBytes(metrics.traffic)]].map(([label, value]) => <Field key={label} label={label} value={value} />)}</div>
    <div className="core-card-protocols"><Kicker>{t('pages.cores.fidelity.protocols')}</Kicker><div>{descriptor?.kinds.map((kind) => <Tag key={kind} color="purple">{kind}</Tag>) ?? '—'}</div></div>
    <div className="core-card-runtime"><Field label={t('pages.cores.fidelity.memory')} value="—" /><Field label={t('pages.cores.columns.restarts')} value={coreRestarts(core) ?? '—'} /></div>
    <CoreActions {...props} card />
  </PanelCard></article>;
}
