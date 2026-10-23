import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Popover, Progress, Tag, Tooltip } from 'antd';
import { EyeInvisibleOutlined, EyeOutlined, LoadingOutlined } from '@ant-design/icons';
import { SizeFormatter } from '@/utils';
import type { OutboundTestState, OutboundTrafficRow } from '@/hooks/useXraySetting';
import type { OutboundRow } from './outbounds-tab-types';
import { isBuiltinOutbound, isUntestable, observedTraffic, outboundAddresses, showSecurity } from './outbounds-tab-helpers';
import TestResultPopover from './TestResultPopover';
import CountryPill from './CountryPill';
import { countryFlag, countryName } from './outbounds-tab-helpers';

export function OutboundProtocolCell({ row }: { row: OutboundRow }) {
  const { t } = useTranslation();
  return <div className="outbound-protocol">
    <Tag color={row.egress ? 'geekblue' : 'purple'}>{row.protocol || '—'}</Tag>
    {isBuiltinOutbound(row) && <span className="p-sub">{t('uiRepair.outbounds.builtin')}</span>}
    {row.streamSettings?.network && <span className="p-sub">{row.streamSettings.network}
      {showSecurity(row.streamSettings.security) ? ` · ${row.streamSettings.security}` : ''}</span>}
  </div>;
}

export function OutboundDestinationCell({ row }: { row: OutboundRow }) {
  const { t } = useTranslation();
  const addresses = outboundAddresses(row);
  const local = row.protocol?.toLowerCase() === 'freedom' ? t('uiRepair.outbounds.directDestination')
    : row.protocol?.toLowerCase() === 'blackhole' ? t('uiRepair.outbounds.blockDestination') : '—';
  return <div className="outbound-destination">
    {addresses.length ? addresses.map((address) => <Tooltip key={address} title={address}><span className="p-mono">{address}</span></Tooltip>)
      : <span className="p-sub">{local}</span>}
  </div>;
}

export function OutboundHealthCell({ row, state }: { row: OutboundRow; state?: OutboundTestState }) {
  const { t, i18n } = useTranslation();
  const [showIP, setShowIP] = useState(false);
  if (state?.testing) return <span className="outbound-health"><LoadingOutlined />{t('loading')}</span>;
  if (row.egress && !row.egress.enable) return <span className="p-sub">{t('disabled')}</span>;
  if (isUntestable(row)) return <Tooltip title={t('uiRepair.outbounds.notProbeable')}><span className="p-sub">—</span></Tooltip>;
  const result = state?.result;
  if (!result) return <span className="p-sub">{t('uiRepair.outbounds.untested')}</span>;
  const egress = result.egress;
  return <div className="outbound-health">
    <TestResultPopover result={result} />
    {egress && <Popover trigger={['hover', 'click']} content={<div className="outbound-egress-details">
      {egress.country && <CountryPill flag={countryFlag(egress.country)} name={countryName(egress.country, i18n.language)} warp={egress.warp} />}
      <Button type="text" size="small" icon={showIP ? <EyeInvisibleOutlined /> : <EyeOutlined />} onClick={() => setShowIP(!showIP)}>{t('pages.index.toggleIpVisibility')}</Button>
      {[egress.ipv4, egress.ipv6].filter(Boolean).map((ip) => <code key={ip}>{showIP ? ip : '••••••'}</code>)}
    </div>}><Button type="text" size="small" aria-label={t('pages.xray.outbound.egress')} icon={<EyeOutlined />} /></Popover>}
  </div>;
}

export function OutboundTrafficCell({ row, traffic }: { row: OutboundRow; traffic: OutboundTrafficRow[] }) {
  const { t } = useTranslation();
  const observed = observedTraffic(traffic, row);
  if (!observed) return <span className="p-sub">{t('uiRepair.outbounds.notReported')}</span>;
  const total = [...new Map(traffic.map((item) => [item.tag, item])).values()].reduce((sum, item) => sum + item.up + item.down, 0);
  const used = observed.up + observed.down;
  const share = total > 0 ? used / total * 100 : undefined;
  return <Popover trigger={['hover', 'click']} content={<div>
    <div>↑ {SizeFormatter.sizeFormat(observed.up)} · ↓ {SizeFormatter.sizeFormat(observed.down)}</div>
    <div>{t('uiRepair.outbounds.trafficScope')}</div>
  </div>}>
    <div className="outbound-traffic"><span>{SizeFormatter.sizeFormat(used)}</span>
      {share !== undefined && <><span className="p-sub">{share.toFixed(1)}%</span><Progress percent={share} showInfo={false} size="small" /></>}
    </div>
  </Popover>;
}

export function OutboundReferencesCell({ references }: { references?: string[] }) {
  const { t } = useTranslation();
  if (!references) return <span className="p-sub">—</span>;
  if (!references.length) return <span className="p-sub">{t('uiRepair.outbounds.unused')}</span>;
  return <Popover trigger={['hover', 'click']} content={<ul className="outbound-rule-list">{references.map((label, index) => <li key={`${index}:${label}`}>{label}</li>)}</ul>}>
    <Button type="text" size="small" aria-label={t('uiRepair.outbounds.ruleReferences')}>{references.length}</Button>
  </Popover>;
}
