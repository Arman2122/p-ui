import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Dropdown, Input, Popover, Space, Switch, Table, Tag, Tooltip, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ArrowDownOutlined, ArrowUpOutlined, CopyOutlined, DeleteOutlined, EditOutlined, GlobalOutlined, MoreOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
import type { HostRecord } from '@/api/queries/useHostsQuery';
import type { InboundOption } from '@/schemas/client';
import { PageHeader, PanelCard, Toolbar } from '@/components/ui/panel';
import { ClipboardManager } from '@/utils';
import './HostList.css';

interface HostListProps {
  hosts: HostRecord[];
  inboundOptions: InboundOption[];
  loading?: boolean;
  isMobile?: boolean;
  selectedGroupIds: string[];
  onSelectionChange: (groupIds: string[]) => void;
  onAdd: () => void;
  onEdit: (host: HostRecord) => void;
  onDelete: (host: HostRecord) => void;
  onToggleEnable: (host: HostRecord, next: boolean) => void;
  onMove: (host: HostRecord, dir: 'up' | 'down') => void;
  onBulkEnable: (enable: boolean) => void;
  onBulkDelete: () => void;
}

const INBOUND_PROTOCOL_COLORS: Record<string, string> = {
  vless: 'blue', vmess: 'geekblue', trojan: 'volcano', shadowsocks: 'magenta', hysteria: 'cyan', hysteria2: 'green', wireguard: 'gold', wgkernel: 'gold', http: 'purple', mixed: 'lime', tunnel: 'orange',
};

export function sortHosts(hosts: HostRecord[]): HostRecord[] {
  return [...hosts].sort((left, right) => (left.sortOrder ?? 0) - (right.sortOrder ?? 0) || (left.remark || '').localeCompare(right.remark || ''));
}

export default function HostList(props: HostListProps) {
  const { t } = useTranslation();
  const { hosts, inboundOptions, loading, selectedGroupIds, onSelectionChange, onAdd, onEdit, onDelete, onToggleEnable, onMove, onBulkEnable, onBulkDelete } = props;
  const [messageApi, messageContext] = message.useMessage();
  const inboundsMap = useMemo(() => new Map(inboundOptions.map(inbound => [inbound.id, inbound])), [inboundOptions]);
  const [search, setSearch] = useState('');
  const sorted = useMemo(() => sortHosts(hosts), [hosts]);
  const shown = sorted.filter(host => `${host.remark ?? ''} ${host.sni ?? ''} ${host.serverDescription ?? ''} ${(host.hosts ?? []).join(' ')} ${(host.tags ?? []).join(' ')}`.toLowerCase().includes(search.toLowerCase()));
  const addresses = (host: HostRecord) => host.hosts.filter(address => address.trim() !== '');
  const endpoint = (address: string, port: number | undefined) => !port || address.includes(':') ? address : `${address}:${port}`;
  const copyAddresses = async (host: HostRecord) => {
    const copied = await ClipboardManager.copyText(addresses(host).map(address => endpoint(address, host.port)).join('\n'));
    if (copied) messageApi.success(t('copied'));
    else messageApi.error(t('somethingWentWrong'));
  };
  const columns: ColumnsType<HostRecord> = [
    { title: t('pages.hosts.fields.enable'), key: 'enable', width: 58, render: (_, host) => <Switch size="small" aria-label={`${t('enable')}: ${host.remark}`} checked={!host.isDisabled} onChange={next => onToggleEnable(host, next)} /> },
    { title: t('pages.hosts.fields.remark'), key: 'host', width: 230, render: (_, host) => {
      const endpoints = addresses(host);
      return <div className="host-remark-cell">
        <span className="p-name">{host.remark || host.groupId}</span>
        <div className="p-mono host-endpoint" title={t('pages.hosts.fields.endpoint')}>{endpoints.length ? endpoint(endpoints[0], host.port) : t('pages.hosts.fields.inheritAddress')}
          {endpoints.length > 1 && <Popover trigger="click" content={<div className="host-address-overflow">{endpoints.slice(1).map(address => <span className="p-mono" key={address}>{endpoint(address, host.port)}</span>)}</div>}><Button type="text" size="small" aria-label={t('pages.hosts.fields.address')}>+{endpoints.length - 1}</Button></Popover>}
        </div>
        {host.port && (!endpoints.length || endpoints.some(address => address.includes(':'))) ? <span className="p-sub">{t('pages.hosts.fields.port')}: {host.port}</span> : null}
        {host.serverDescription && <span className="p-sub">{host.serverDescription}</span>}
        {!!host.tags?.length && <Space size={4} wrap>{host.tags.map(tag => <Tag key={tag}>{tag}</Tag>)}</Space>}
      </div>;
    } },
    { title: t('pages.hosts.fields.inbound'), key: 'inbound', width: 152, render: (_, host) => {
      const chip = (id: number) => {
        const inbound = inboundsMap.get(id);
        return <Tag color={INBOUND_PROTOCOL_COLORS[inbound?.protocol ?? '']} key={id}>{inbound?.remark || inbound?.tag || `#${id}`}</Tag>;
      };
      return host.inboundIds.length ? <Space size={4} wrap>{chip(host.inboundIds[0])}{host.inboundIds.length > 1 && <Popover trigger="click" content={<Space direction="vertical" size={4}>{host.inboundIds.slice(1).map(chip)}</Space>}><Button type="text" size="small" aria-label={t('pages.hosts.fields.inbound')}>+{host.inboundIds.length - 1}</Button></Popover>}</Space> : <span className="p-sub">—</span>;
    } },
    { title: t('pages.hosts.fields.security'), key: 'security', width: 122, render: (_, host) => <Tag color={host.security === 'reality' ? 'green' : host.security === 'tls' ? 'blue' : undefined}>{host.security || 'same'}</Tag> },
    { title: t('pages.hosts.fields.sni'), key: 'sni', width: 190, render: (_, host) => <span className="p-mono">{host.sni || '—'}</span> },
    { title: <span className="host-actions-label">{t('pages.hosts.fields.actions')}</span>, key: 'actions', width: 140, align: 'right', render: (_, host) => {
      const index = sorted.findIndex(item => item.groupId === host.groupId);
      return <Space size={2}>
        <Tooltip title={t('designParityHosts.copyAddress', { defaultValue: 'Copy addresses' })}><Button size="small" type="text" icon={<CopyOutlined />} disabled={!addresses(host).length} aria-label={t('designParityHosts.copyAddress', { defaultValue: 'Copy addresses' })} onClick={() => { void copyAddresses(host); }} /></Tooltip>
        <Tooltip title={t('edit')}><Button size="small" type="text" icon={<EditOutlined />} aria-label={t('edit')} onClick={() => onEdit(host)} /></Tooltip>
        <Tooltip title={t('delete')}><Button size="small" type="text" danger icon={<DeleteOutlined />} aria-label={t('delete')} onClick={() => onDelete(host)} /></Tooltip>
        <Dropdown trigger={['click']} menu={{ items: [
          { key: 'up', label: t('pages.hosts.moveUp'), icon: <ArrowUpOutlined aria-hidden />, disabled: index === 0, onClick: () => onMove(host, 'up') },
          { key: 'down', label: t('pages.hosts.moveDown'), icon: <ArrowDownOutlined aria-hidden />, disabled: index === sorted.length - 1, onClick: () => onMove(host, 'down') },
        ] }}><Button type="text" size="small" icon={<MoreOutlined />} aria-label={t('more')} /></Dropdown>
      </Space>;
    } },
  ];
  return <div className="host-list-page">
    {messageContext}
    <PageHeader title={t('menu.hosts')} sub={t('pages.hosts.subtitle')} actions={<Button type="primary" icon={<PlusOutlined />} onClick={onAdd}>{t('pages.hosts.addHost')}</Button>} />
    <PanelCard pad={0} className="hosts-card">
      <Toolbar left={<Input type="search" prefix={<SearchOutlined />} value={search} onChange={event => setSearch(event.target.value)} allowClear placeholder={t('designParityHosts.search', { defaultValue: 'Search host, SNI…' })} aria-label={t('search')} />}
        right={<Tooltip title={`${t('pages.hosts.summary.total')}: ${hosts.length} · ${t('pages.hosts.summary.enabled')}: ${hosts.filter(host => !host.isDisabled).length} · ${t('pages.hosts.summary.disabled')}: ${hosts.filter(host => host.isDisabled).length}`}><span className="p-sub">{t('designParityHosts.count', { count: shown.length, defaultValue: '{count} hosts' })}</span></Tooltip>} />
      {selectedGroupIds.length > 0 && <div className="host-bulk-toolbar">
        <Tag closable onClose={() => onSelectionChange([])}>{t('pages.hosts.selectedCount', { count: selectedGroupIds.length })}</Tag>
        <Button size="small" onClick={() => onBulkEnable(true)}>{t('pages.hosts.bulkEnable')}</Button>
        <Button size="small" onClick={() => onBulkEnable(false)}>{t('pages.hosts.bulkDisable')}</Button>
        <Button size="small" danger icon={<DeleteOutlined />} onClick={onBulkDelete}>{t('pages.hosts.bulkDelete')}</Button>
      </div>}
      <Table<HostRecord> className="p-table" rowKey="groupId" size="middle" loading={loading} columns={columns} dataSource={shown} pagination={false} scroll={{ x: 900 }}
        rowSelection={{ columnWidth: 38, selectedRowKeys: selectedGroupIds, onChange: keys => onSelectionChange(keys as string[]) }}
        locale={{ emptyText: <div className="card-empty"><GlobalOutlined /><div>{t('noData')}</div></div> }} />
    </PanelCard>
  </div>;
}
