import { useTranslation } from 'react-i18next';
import { Button, Dropdown, Tooltip } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined, DeleteOutlined, EditOutlined, MoreOutlined, RetweetOutlined, ThunderboltOutlined, VerticalAlignTopOutlined } from '@ant-design/icons';
import type { EgressRecord } from '@/schemas/api/egress';
import type { OutboundTestMode } from '@/hooks/useXraySetting';
import type { OutboundRow } from './outbounds-tab-types';
import { effectiveTestMode, isBuiltinOutbound, isUntestable, testModeLabel } from './outbounds-tab-helpers';

export interface OutboundActionHandlers {
  openEdit: (index: number) => void;
  setFirst: (index: number) => void;
  moveUp: (index: number) => void;
  moveDown: (index: number) => void;
  confirmDelete: (index: number) => void;
  onResetTraffic: (tag: string) => void;
  onTest: (key: number, mode: string) => void;
  editExit: (exit: EgressRecord) => void;
  deleteExit: (exit: EgressRecord) => void;
}

export default function OutboundActions({ row, index, rowCount, testMode, testing, busy, handlers }: {
  row: OutboundRow; index: number; rowCount: number; testMode: OutboundTestMode;
  testing: boolean; busy?: boolean; handlers: OutboundActionHandlers;
}) {
  const { t } = useTranslation();
  const protectedRow = isBuiltinOutbound(row);
  return <div className="outbound-actions">
    <Tooltip title={isUntestable(row) ? t('uiRepair.outbounds.notProbeable') : `${t('check')} (${testModeLabel(effectiveTestMode(row, testMode), t)})`}>
      <Button type="text" size="small" icon={<ThunderboltOutlined />} aria-label={t('check')} loading={testing}
        disabled={isUntestable(row) || testing} onClick={() => handlers.onTest(row.key, testMode)} />
    </Tooltip>
    <Button type="text" size="small" icon={<EditOutlined />} aria-label={t('edit')} disabled={busy}
      onClick={() => row.egress ? handlers.editExit(row.egress) : handlers.openEdit(index)} />
    <Dropdown trigger={['click']} menu={{ items: row.egress ? [
      { key: 'del', danger: true, disabled: busy, label: t('delete'), icon: <DeleteOutlined />, onClick: () => handlers.deleteExit(row.egress!) },
    ] : [
      { key: 'top', label: t('pages.xray.outbound.moveToTop'), icon: <VerticalAlignTopOutlined />, disabled: index === 0, onClick: () => handlers.setFirst(index) },
      { key: 'up', label: t('pages.inbounds.form.moveUp'), icon: <ArrowUpOutlined />, disabled: index === 0, onClick: () => handlers.moveUp(index) },
      { key: 'down', label: t('pages.inbounds.form.moveDown'), icon: <ArrowDownOutlined />, disabled: index === rowCount - 1, onClick: () => handlers.moveDown(index) },
      { key: 'reset', label: t('pages.inbounds.resetTraffic'), icon: <RetweetOutlined />, onClick: () => handlers.onResetTraffic(row.tag || '') },
      { type: 'divider' },
      { key: 'del', danger: true, disabled: protectedRow, label: t('delete'), icon: <DeleteOutlined />, onClick: () => handlers.confirmDelete(index) },
    ] }}><Button type="text" size="small" icon={<MoreOutlined />} aria-label={t('more')} /></Dropdown>
  </div>;
}
