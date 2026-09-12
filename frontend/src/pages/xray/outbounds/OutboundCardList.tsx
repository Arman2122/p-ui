import { useTranslation } from 'react-i18next';
import { Switch } from 'antd';
import type { OutboundRow } from './outbounds-tab-types';
import type { OutboundColumnsParams } from './useOutboundColumns';
import OutboundActions from './OutboundActions';
import { OutboundDestinationCell, OutboundHealthCell, OutboundProtocolCell, OutboundReferencesCell, OutboundTrafficCell } from './OutboundCells';
import { referencedRules } from './outbounds-tab-helpers';

export default function OutboundCardList({ rows, controls }: { rows: OutboundRow[]; controls: OutboundColumnsParams }) {
  const { t } = useTranslation();
  if (!rows.length) return <div className="card-empty">{t('noData')}</div>;
  return <div className="outbound-mobile-list">{rows.map((row, index) => <article key={row.key} className="outbound-card">
    <div className="outbound-name"><span className="outbound-position">{index + 1}</span><span className="p-name">{row.tag}</span>
      {row.egress && <Switch size="small" checked={row.egress.enable} aria-label={`${t('enable')}: ${row.tag}`}
        loading={controls.busyExitId === row.egress.id} disabled={controls.busyExitId !== null} onChange={() => controls.toggleExit(row.egress!)} />}
    </div>
    <OutboundProtocolCell row={row} />
    <OutboundDestinationCell row={row} />
    <div className="outbound-mobile-stats"><OutboundHealthCell row={row} state={controls.outboundTestStates[row.key]} />
      <OutboundTrafficCell row={row} traffic={controls.outboundsTraffic} />
      <OutboundReferencesCell references={referencedRules(controls.templateSettings, row, controls.hostRules)} /></div>
    <OutboundActions row={row} index={index} rowCount={controls.rows.length} testMode={controls.testMode}
      testing={!!controls.outboundTestStates[row.key]?.testing} busy={controls.busyExitId !== null} handlers={controls} />
  </article>)}</div>;
}
