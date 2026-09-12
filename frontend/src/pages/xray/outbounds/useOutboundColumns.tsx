import { useTranslation } from 'react-i18next';
import { Switch, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { OutboundTestMode, OutboundTestState, OutboundTrafficRow, XraySettingsValue } from '@/hooks/useXraySetting';
import type { EgressRecord } from '@/schemas/api/egress';
import type { RoutingRuleRecord } from '@/schemas/api/routing';
import type { OutboundRow } from './outbounds-tab-types';
import OutboundActions, { type OutboundActionHandlers } from './OutboundActions';
import { OutboundDestinationCell, OutboundHealthCell, OutboundProtocolCell, OutboundReferencesCell, OutboundTrafficCell } from './OutboundCells';
import { referencedRules } from './outbounds-tab-helpers';

export interface OutboundColumnsParams extends OutboundActionHandlers {
  testMode: OutboundTestMode;
  rows: OutboundRow[];
  outboundsTraffic: OutboundTrafficRow[];
  outboundTestStates: Record<number, OutboundTestState>;
  templateSettings: XraySettingsValue | null;
  hostRules?: RoutingRuleRecord[];
  busyExitId: number | null;
  toggleExit: (exit: EgressRecord) => void;
}

export function useOutboundColumns(params: OutboundColumnsParams): ColumnsType<OutboundRow> {
  const { t } = useTranslation();
  return [
    { title: t('pages.xray.outbound.tag'), key: 'identity', width: 170, render: (_, row, index) => <div className="outbound-name">
      <span className="outbound-position">{index + 1}</span>
      <Tooltip title={row.tag}><span className="p-name">{row.tag || '—'}</span></Tooltip>
      {row.egress && <Switch size="small" aria-label={`${t('enable')}: ${row.tag}`} checked={row.egress.enable}
        loading={params.busyExitId === row.egress.id} disabled={params.busyExitId !== null}
        onChange={() => params.toggleExit(row.egress!)} />}
    </div> },
    { title: t('protocol'), key: 'protocol', width: 120, render: (_, row) => <OutboundProtocolCell row={row} /> },
    { title: t('uiRepair.outbounds.destination'), key: 'address', width: 200, render: (_, row) => <OutboundDestinationCell row={row} /> },
    { title: t('pages.nodes.latency'), key: 'health', width: 130, render: (_, row) => <OutboundHealthCell row={row} state={params.outboundTestStates[row.key]} /> },
    { title: t('uiRepair.outbounds.measuredTraffic'), key: 'traffic', width: 165, render: (_, row) => <OutboundTrafficCell row={row} traffic={params.outboundsTraffic} /> },
    { title: t('uiRepair.outbounds.rules'), key: 'rules', width: 70, align: 'end', render: (_, row) => <OutboundReferencesCell references={referencedRules(params.templateSettings, row, params.hostRules)} /> },
    { title: '', key: 'actions', width: 130, align: 'end', render: (_, row, index) => <OutboundActions row={row} index={index}
      rowCount={params.rows.length} testMode={params.testMode} testing={!!params.outboundTestStates[row.key]?.testing}
      busy={params.busyExitId !== null} handlers={params} /> },
  ];
}
