import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Table, Tooltip } from 'antd';
import { ThunderboltOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import type { OutboundTestMode, OutboundTestState, OutboundTrafficRow, XraySettingsValue } from '@/hooks/useXraySetting';
import type { RoutingRuleRecord } from '@/schemas/api/routing';
import { PanelCard } from '@/components/ui/panel';
import type { OutboundRow } from './outbounds-tab-types';
import { effectiveTestMode, isUntestable, referencedRules, testModeLabel } from './outbounds-tab-helpers';
import { OutboundDestinationCell, OutboundHealthCell, OutboundProtocolCell, OutboundReferencesCell, OutboundTrafficCell } from './OutboundCells';

interface SubscriptionOutboundsProps {
  subscriptionOutbounds: unknown[];
  outboundsTraffic: OutboundTrafficRow[];
  subscriptionTestStates: Record<string, OutboundTestState>;
  testMode: OutboundTestMode;
  isMobile: boolean;
  templateSettings?: XraySettingsValue | null;
  hostRules?: RoutingRuleRecord[];
  onTestSubscription: (outbound: Record<string, unknown>, mode: string) => void;
}

export default function SubscriptionOutbounds({ subscriptionOutbounds, outboundsTraffic, subscriptionTestStates,
  testMode, isMobile, templateSettings = null, hostRules, onTestSubscription }: SubscriptionOutboundsProps) {
  const { t } = useTranslation();
  const rows = useMemo<OutboundRow[]>(() => subscriptionOutbounds.map((row, key) => ({ ...(row as object), key })), [subscriptionOutbounds]);
  if (!rows.length) return null;
  const probe = (row: OutboundRow) => <Tooltip title={`${t('check')} (${testModeLabel(effectiveTestMode(row, testMode), t)})`}>
    <Button type="text" size="small" icon={<ThunderboltOutlined />} aria-label={t('check')}
      loading={subscriptionTestStates[row.tag || '']?.testing}
      disabled={!row.tag || isUntestable(row) || subscriptionTestStates[row.tag || '']?.testing}
      onClick={() => onTestSubscription(row as unknown as Record<string, unknown>, testMode)} />
  </Tooltip>;
  const columns: ColumnsType<OutboundRow> = [
    { title: t('pages.xray.outbound.tag'), key: 'identity', width: 170, render: (_, row) => <span className="p-name" title={row.tag}>{row.tag || '—'}</span> },
    { title: t('protocol'), key: 'protocol', width: 120, render: (_, row) => <OutboundProtocolCell row={row} /> },
    { title: t('uiRepair.outbounds.destination'), key: 'address', width: 200, render: (_, row) => <OutboundDestinationCell row={row} /> },
    { title: t('pages.nodes.latency'), key: 'health', width: 130, render: (_, row) => <OutboundHealthCell row={row} state={subscriptionTestStates[row.tag || '']} /> },
    { title: t('uiRepair.outbounds.measuredTraffic'), key: 'traffic', width: 165, render: (_, row) => <OutboundTrafficCell row={row} traffic={outboundsTraffic} /> },
    { title: t('uiRepair.outbounds.rules'), key: 'rules', width: 70, render: (_, row) => <OutboundReferencesCell references={referencedRules(templateSettings, row, hostRules)} /> },
    { title: '', key: 'test', width: 50, align: 'end', render: (_, row) => probe(row) },
  ];
  return <PanelCard pad={0} className="outbounds-card subscription-outbounds" title={t('pages.xray.outboundSub.fromSubsTitle')}>
    <div className="outbounds-note">{t('pages.xray.outboundSub.fromSubsDesc')}</div>
    {isMobile ? <div className="outbound-mobile-list">{rows.map((row) => <article className="outbound-card" key={row.key}>
      <div className="outbound-name"><span className="p-name">{row.tag}</span>{probe(row)}</div>
      <OutboundProtocolCell row={row} /><OutboundDestinationCell row={row} />
      <div className="outbound-mobile-stats"><OutboundHealthCell row={row} state={subscriptionTestStates[row.tag || '']} />
        <OutboundTrafficCell row={row} traffic={outboundsTraffic} />
        <OutboundReferencesCell references={referencedRules(templateSettings, row, hostRules)} /></div>
    </article>)}</div> : <Table className="p-table outbounds-table" columns={columns} dataSource={rows} rowKey="key"
      pagination={false} size="small" tableLayout="fixed" scroll={{ x: 905 }} />}
  </PanelCard>;
}
