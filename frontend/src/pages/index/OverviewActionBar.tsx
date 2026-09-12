import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Dropdown, Tag } from 'antd';
import {
  ArrowUpOutlined,
  AreaChartOutlined,
  BarsOutlined,
  CloudDownloadOutlined,
  CloudServerOutlined,
  ControlOutlined,
  FileTextOutlined,
  MoreOutlined,
  PoweroffOutlined,
  ReloadOutlined,
} from '@ant-design/icons';

import { PageHeader } from '@/components/ui/panel';
import { formatPanelVersion } from '@/lib/panel-version';
import type { Status } from '@/models/status';

interface OverviewActionBarProps {
  historyControl?: ReactNode;
  status: Status;
  isMobile: boolean;
  accessLogEnable: boolean;
  panelVersion: string;
  latestVersion: string;
  updateAvailable: boolean;
  onRefresh: () => void;
  onStopXray: () => void;
  onRestartXray: () => void;
  onOpenLogs: () => void;
  onOpenXrayLogs: () => void;
  onOpenConfig: () => void;
  onOpenBackup: () => void;
  onOpenSystemHistory: () => void;
  onOpenXrayMetrics: () => void;
  onOpenPanelUpdate: () => void;
  onOpenVersionSwitch: () => void;
}

interface BarAction {
  key: string;
  icon: ReactNode;
  text: string;
  onClick: () => void;
  primary?: boolean;
}

export default function OverviewActionBar({
  historyControl,
  status,
  isMobile,
  accessLogEnable,
  panelVersion,
  latestVersion,
  updateAvailable,
  onRefresh,
  onStopXray,
  onRestartXray,
  onOpenLogs,
  onOpenXrayLogs,
  onOpenConfig,
  onOpenBackup,
  onOpenSystemHistory,
  onOpenXrayMetrics,
  onOpenPanelUpdate,
  onOpenVersionSwitch,
}: OverviewActionBarProps) {
  const { t } = useTranslation();
  const size = isMobile ? ('small' as const) : ('middle' as const);

  const actionGroups: BarAction[][] = [
    [
      { key: 'restart', icon: <ReloadOutlined />, text: t('pages.index.restartXray'), onClick: onRestartXray, primary: true },
      { key: 'stop', icon: <PoweroffOutlined />, text: t('pages.index.stopXray'), onClick: onStopXray },
    ],
    [
      { key: 'logs', icon: <BarsOutlined />, text: t('pages.index.logs'), onClick: onOpenLogs },
      ...(accessLogEnable
        ? [{ key: 'accessLogs', icon: <FileTextOutlined />, text: t('pages.index.accessLogs'), onClick: onOpenXrayLogs }]
        : []),
      { key: 'config', icon: <ControlOutlined />, text: t('pages.index.config'), onClick: onOpenConfig },
      { key: 'backup', icon: <CloudServerOutlined />, text: t('pages.index.backupTitle'), onClick: onOpenBackup },
    ],
    [
      { key: 'history', icon: <AreaChartOutlined />, text: t('pages.index.systemHistoryTitle'), onClick: onOpenSystemHistory },
      { key: 'metrics', icon: <ArrowUpOutlined />, text: t('pages.index.xrayMetricsTitle'), onClick: onOpenXrayMetrics },
      { key: 'version', icon: <ControlOutlined />, text: `${t('pages.index.xraySwitch')} · ${status.xray.version || '—'}`, onClick: onOpenVersionSwitch },
    ],
  ];

  return <PageHeader title={t('menu.dashboard')} sub={`${window.location.hostname} · ${t('dashboardRepair.subtitle')}`} actions={<>
    {historyControl}
    {updateAvailable && <Tag color="warning" icon={<CloudDownloadOutlined />} onClick={onOpenPanelUpdate}>
      {`${t('update')} ${formatPanelVersion(latestVersion)}`}
    </Tag>}
    <Dropdown trigger={['click']} menu={{ items: [
      ...actionGroups.flat().map((action) => ({ key: action.key, icon: action.icon, label: action.text, onClick: action.onClick })),
      { key: 'panelUpdate', label: `${t('pages.index.updatePanel')} · ${formatPanelVersion(panelVersion)}`, onClick: onOpenPanelUpdate },
    ] }}><Button size={size} type="text" icon={<MoreOutlined />} aria-label={t('dashboardRepair.panelActions')} /></Dropdown>
    <Button size={size} icon={<ReloadOutlined />} onClick={onRefresh}>{t('refresh')}</Button>
  </>} />;
}
