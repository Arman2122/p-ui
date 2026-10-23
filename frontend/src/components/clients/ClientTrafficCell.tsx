import { memo, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Popover, Progress } from 'antd';

import InfinityIcon from '@/components/ui/InfinityIcon';
import { useTheme } from '@/hooks/useTheme';
import { computeTrafficDisplay } from '@/lib/clients/traffic-display';
import type { ClientTraffic } from '@/schemas/client';
import { IntlUtil, SizeFormatter } from '@/utils';
import './ClientTrafficCell.css';

export interface ClientTrafficCellProps {
  usedTotal?: number;
  up?: number;
  down?: number;
  total?: number;
  enabled?: boolean;
  trafficDiff?: number;
  compact?: boolean;
  accounting?: ClientTraffic['accounting'];
}

const ClientTrafficCell = memo(function ClientTrafficCell({
  usedTotal,
  up = 0,
  down = 0,
  total,
  enabled = true,
  trafficDiff = 0,
  compact = false,
  accounting,
}: ClientTrafficCellProps) {
  const { t } = useTranslation();
  const { isDark } = useTheme();

  const display = useMemo(
    () => computeTrafficDisplay({ up: usedTotal ?? up, down: usedTotal === undefined ? down : 0, total: total ?? 0, enabled, trafficDiff }, isDark),
    [up, down, usedTotal, total, enabled, trafficDiff, isDark],
  );
  const fleet = accounting?.source === 'fleet' ? accounting : null;
  const unknownQuota = !!fleet && total == null;
  const amount = (bytes: number | null | undefined) => bytes == null ? t('pages.clients.accounting.unknown') : SizeFormatter.sizeFormat(bytes);

  const popover = (
    <table className="client-traffic-popover">
      <tbody>
        {fleet ? <>
          <tr><td colSpan={2}>{t('pages.clients.accounting.measured')}</td><td>↑ {amount(fleet.measuredUploaded)}</td><td>↓ {amount(fleet.measuredDownloaded)}</td></tr>
          <tr><td colSpan={2}>{t('pages.clients.accounting.opening')}</td><td>↑ {amount(fleet.openingUploaded)}</td><td>↓ {amount(fleet.openingDownloaded)}</td></tr>
          <tr><td colSpan={2}>{t('pages.clients.accounting.reserved')}</td><td colSpan={2}>{amount(fleet.reservedBytes)}</td></tr>
          <tr><td colSpan={2}>{t('pages.clients.accounting.uncertain')}</td><td colSpan={2}>{amount(fleet.uncertainBytes)}</td></tr>
          <tr><td colSpan={2}>{t('pages.clients.accounting.reportedAt')}</td><td colSpan={2}>{fleet.reportedUnixMilli > 0 ? IntlUtil.formatDate(fleet.reportedUnixMilli) : t('pages.clients.accounting.unknown')}</td></tr>
          <tr><td colSpan={2}>{t('pages.clients.accounting.observedAt')}</td><td colSpan={2}>{fleet.observedUnixMilli > 0 ? IntlUtil.formatDate(fleet.observedUnixMilli) : t('pages.clients.accounting.unknown')}</td></tr>
          {fleet.state === 'awaiting_activation' && <tr><td colSpan={4}>{t('pages.clients.accounting.awaitingActivation')}</td></tr>}
        </> : usedTotal === undefined ? <tr>
          <td>↑</td>
          <td>{SizeFormatter.sizeFormat(up)}</td>
          <td>↓</td>
          <td>{SizeFormatter.sizeFormat(down)}</td>
        </tr> : <tr>
          <td colSpan={2}>{t('pages.clients.traffic')}</td>
          <td colSpan={2}>{SizeFormatter.sizeFormat(display.used)}</td>
        </tr>}
        {!unknownQuota && !display.isUnlimited && (
          <tr>
            <td colSpan={2}>{t('pages.clients.remaining')}</td>
            <td colSpan={2}>{SizeFormatter.sizeFormat(display.remaining)}</td>
          </tr>
        )}
      </tbody>
    </table>
  );

  const rootClass = [
    'client-traffic-cell',
    compact ? 'is-compact' : '',
    display.isUnlimited && !unknownQuota ? 'is-unlimited' : '',
  ].filter(Boolean).join(' ');

  return (
    <Popover content={popover} trigger={['hover', 'click']} placement="top">
      <div className={rootClass}>
        <span className="client-traffic-cell-used">{SizeFormatter.sizeFormat(display.used)}</span>
        {!unknownQuota && <Progress
          className="client-traffic-cell-bar"
          aria-label={`${SizeFormatter.sizeFormat(display.used)} / ${display.isUnlimited ? t('subscription.unlimited') : SizeFormatter.sizeFormat(total ?? 0)}`}
          percent={display.percent}
          showInfo={false}
          strokeColor={display.strokeColor}
          status={display.status}
          size={compact ? 'small' : 'medium'}
        />}
        <span className="client-traffic-cell-limit">
          {unknownQuota ? t('pages.clients.accounting.unknown') : display.isUnlimited ? (
            <span className="client-traffic-cell-infinity" role="img" aria-label={t('subscription.unlimited')}>
              <InfinityIcon />
            </span>
          ) : (
            SizeFormatter.sizeFormat(total ?? 0)
          )}
        </span>
      </div>
    </Popover>
  );
});

export default ClientTrafficCell;
