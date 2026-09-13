import { lazy, useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Dropdown,
  Modal,
  Result,
  Space,
  Spin,
  Tag,
  message,
} from 'antd';
import type { MenuProps } from 'antd';
import {
  ClockCircleOutlined,
  DownloadOutlined,
  InfoCircleOutlined,
  DeleteOutlined,
  EditOutlined,
  LinkOutlined,
  MoreOutlined,
  PlusOutlined,
  RetweetOutlined,
  UsergroupAddOutlined,
  UsergroupDeleteOutlined,
} from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { usePageTitle } from '@/hooks/usePageTitle';
import { useClients } from '@/hooks/useClients';
import { HttpUtil, SizeFormatter, FileManager } from '@/utils';
import GroupQuotaModal from './GroupQuotaModal';
import GroupFormModal from './GroupFormModal';
import './groups-parity.css';
import { inboundTone } from '../policies/inbound-tone';
import { ClientTrendCell } from '../clients/ClientMetadataCells';
import { PageHeader, PanelCard, Field, Meter } from '@/components/ui/panel';
import { setMessageInstance } from '@/utils/messageBus';
import { LazyMount } from '@/components/utility';
import { keys } from '@/api/queryKeys';
import { useInboundOptions } from '@/api/queries/useInboundOptions';
import {
  ClientRecordSchema,
  GroupSummaryListSchema,
  type ClientRecord,
  type GroupSummary,
} from '@/schemas/client';
import { parseMsg } from '@/utils/zodValidate';

const ClientRecordListSchema = z.array(ClientRecordSchema).nullable().transform((v) => v ?? []);

const SubLinksModal = lazy(() => import('../clients/SubLinksModal'));
const ClientBulkAdjustModal = lazy(() => import('../clients/ClientBulkAdjustModal'));
const GroupAddClientsModal = lazy(() => import('./GroupAddClientsModal'));
const GroupRemoveClientsModal = lazy(() => import('./GroupRemoveClientsModal'));

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

async function fetchGroups(): Promise<GroupSummary[]> {
  const msg = await HttpUtil.get('/panel/api/clients/groups', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load groups');
  const validated = parseMsg(msg, GroupSummaryListSchema, 'clients/groups');
  return validated.obj ?? [];
}

async function fetchEmailsForGroup(name: string): Promise<string[]> {
  const msg = await HttpUtil.get<string[]>(
    `/panel/api/clients/groups/${encodeURIComponent(name)}/emails`,
    undefined,
    { silent: true },
  );
  if (!msg?.success || !Array.isArray(msg.obj)) return [];
  return msg.obj;
}

export default function GroupsPage() {
  usePageTitle();
  const { t } = useTranslation();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => { setMessageInstance(messageApi); }, [messageApi]);
  const queryClient = useQueryClient();

  const { subSettings, bulkAdjust, bulkAddToGroup, bulkRemoveFromGroup, bulkDelete } = useClients({ list: false });
  const inboundQuery = useInboundOptions();
  const inbounds = inboundQuery.data ?? [];

  const groupsQuery = useQuery({
    queryKey: keys.clients.groups(),
    queryFn: fetchGroups,
  });
  const groups = useMemo(() => groupsQuery.data ?? [], [groupsQuery.data]);
  const loading = groupsQuery.isFetching;
  const fetched = groupsQuery.data !== undefined || groupsQuery.isError;
  const fetchError = groupsQuery.error ? (groupsQuery.error as Error).message : '';

  const invalidate = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: keys.clients.root() });
  }, [queryClient]);

  const deleteMut = useMutation({
    mutationFn: (body: { name: string }) =>
      HttpUtil.post('/panel/api/clients/groups/delete', body, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const groupResetMut = useMutation({
    mutationFn: (body: { name: string }) =>
      HttpUtil.post('/panel/api/clients/groups/resetTraffic', body, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidate(); },
  });

  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<GroupSummary | null>(null);
  const [detailGroup, setDetailGroup] = useState<GroupSummary | null>(null);
  const [quotaGroup, setQuotaGroup] = useState<GroupSummary | null>(null);

  const [subLinksOpen, setSubLinksOpen] = useState(false);
  const [adjustOpen, setAdjustOpen] = useState(false);
  const [addClientsOpen, setAddClientsOpen] = useState(false);
  const [removeClientsOpen, setRemoveClientsOpen] = useState(false);
  const [groupEmails, setGroupEmails] = useState<string[]>([]);
  const [groupForAction, setGroupForAction] = useState<GroupSummary | null>(null);

  const allClientsQuery = useQuery<ClientRecord[]>({
    queryKey: keys.clients.all(),
    queryFn: async () => {
      const msg = await HttpUtil.get('/panel/api/clients/list', undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Failed to load clients');
      const validated = parseMsg(msg, ClientRecordListSchema, 'clients/list');
      return validated.obj ?? [];
    },
    enabled: addClientsOpen || removeClientsOpen || subLinksOpen,
    staleTime: 30_000,
  });
  const allClients = allClientsQuery.data ?? [];

  function openCreate() { setEditing(null); setFormOpen(true); }
  function openRename(group: GroupSummary) { setEditing(group); setFormOpen(true); }

  function onDelete(g: GroupSummary) {
    modal.confirm({
      title: t('pages.groups.deleteConfirmTitle', { name: g.name }),
      content: t('pages.groups.deleteConfirmContent', { count: g.clientCount }),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await deleteMut.mutateAsync({ name: g.name });
        if (msg?.success) {
          const affected = (msg.obj as { affected?: number } | undefined)?.affected ?? 0;
          messageApi.success(t('pages.groups.deleteSuccess', { count: affected }));
        }
      },
    });
  }

  async function openSubLinksFor(g: GroupSummary) {
    if (!g.clientCount) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    const emails = await fetchEmailsForGroup(g.name);
    if (emails.length === 0) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    setGroupForAction(g);
    setGroupEmails(emails);
    setSubLinksOpen(true);
  }

  async function openAdjustFor(g: GroupSummary) {
    if (!g.clientCount) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    const emails = await fetchEmailsForGroup(g.name);
    if (emails.length === 0) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    setGroupForAction(g);
    setGroupEmails(emails);
    setAdjustOpen(true);
  }

  function openAddClientsFor(g: GroupSummary) {
    setGroupForAction(g);
    setAddClientsOpen(true);
  }

  function openRemoveClientsFor(g: GroupSummary) {
    if (!g.clientCount) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    setGroupForAction(g);
    setRemoveClientsOpen(true);
  }

  function onDeleteClients(g: GroupSummary) {
    if (!g.clientCount) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    modal.confirm({
      title: t('pages.groups.deleteClientsConfirmTitle', { name: g.name }),
      content: t('pages.groups.deleteClientsConfirmContent', { count: g.clientCount }),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const emails = await fetchEmailsForGroup(g.name);
        if (emails.length === 0) return;
        const msg = await bulkDelete(emails);
        if (msg?.success) {
          const ok = msg.obj?.deleted ?? 0;
          const skipped = msg.obj?.skipped ?? [];
          const failed = skipped.length;
          if (failed === 0) {
            messageApi.success(t('pages.groups.deleteClientsSuccess', { count: ok }));
          } else {
            const firstError = skipped[0]?.reason ?? msg?.msg ?? '';
            messageApi.warning(firstError
              ? `${t('pages.groups.deleteClientsMixed', { ok, failed })} — ${firstError}`
              : t('pages.groups.deleteClientsMixed', { ok, failed }));
          }
        }
      },
    });
  }

  function onResetTraffic(g: GroupSummary) {
    if (!g.clientCount) {
      messageApi.info(t('pages.groups.emptyForAction'));
      return;
    }
    modal.confirm({
      title: t('pages.groups.resetConfirmTitle', { name: g.name }),
      content: t('pages.groups.resetConfirmContent'),
      okText: t('reset'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await groupResetMut.mutateAsync({ name: g.name });
        if (msg?.success) {
          messageApi.success(t('pages.groups.resetSuccess', { name: g.name }));
        }
      },
    });
  }

  function rowActions(row: GroupSummary): MenuProps['items'] {
    return [
      { key: 'details', icon: <InfoCircleOutlined />, label: t('groupParity.details', { defaultValue: 'Group details' }), onClick: () => setDetailGroup(row) },
      { key: 'quota', label: t('telemetry.groupQuota'), onClick: () => setQuotaGroup(row) },
      {
        key: 'subLinks',
        icon: <LinkOutlined />,
        label: t('pages.clients.subLinksSelected', { count: row.clientCount || 0 }),
        disabled: !row.clientCount,
        onClick: () => openSubLinksFor(row),
      },
      {
        key: 'adjust',
        icon: <ClockCircleOutlined />,
        label: t('pages.clients.adjustSelected', { count: row.clientCount || 0 }),
        disabled: !row.clientCount,
        onClick: () => openAdjustFor(row),
      },
      {
        key: 'reset',
        icon: <RetweetOutlined />,
        label: t('pages.groups.resetTraffic'),
        disabled: !row.clientCount,
        onClick: () => onResetTraffic(row),
      },
      {
        key: 'addClients',
        icon: <UsergroupAddOutlined />,
        label: t('pages.groups.addToGroup'),
        onClick: () => openAddClientsFor(row),
      },
      {
        key: 'rename',
        icon: <EditOutlined />,
        label: t('pages.groups.rename'),
        onClick: () => openRename(row),
      },
      { type: 'divider' },
      {
        key: 'removeClients',
        icon: <UsergroupDeleteOutlined />,
        label: t('pages.groups.removeFromGroup'),
        danger: true,
        disabled: !row.clientCount,
        onClick: () => openRemoveClientsFor(row),
      },
      {
        key: 'deleteClients',
        icon: <DeleteOutlined />,
        label: t('pages.groups.deleteClients'),
        danger: true,
        disabled: !row.clientCount,
        onClick: () => onDeleteClients(row),
      },
      {
        key: 'delete',
        icon: <DeleteOutlined />,
        label: t('pages.groups.deleteGroupOnly'),
        danger: true,
        onClick: () => onDelete(row),
      },
    ];
  }

  return <div className="groups-parity">
    {messageContextHolder}{modalContextHolder}
    <PageHeader title={t('menu.groups')} sub={t('pages.groups.subtitle')} actions={<Space size={8} wrap>
      <Button icon={<DownloadOutlined />} onClick={() => FileManager.downloadTextFile(JSON.stringify(groups, null, 2), 'group-usage.json')}>{t('telemetry.exportUsage')}</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('pages.groups.addGroup')}</Button>
    </Space>} />
    {inboundQuery.isError && <Alert type="error" title={t('somethingWentWrong')} description={inboundQuery.error.message} />}
    <Spin spinning={!fetched} delay={200} description={t('loading')}>
      {fetchError ? <Result status="error" title={t('somethingWentWrong')} subTitle={fetchError} extra={<Button loading={loading} onClick={() => groupsQuery.refetch()}>{t('refresh')}</Button>} /> :
        <div className="groups-grid">{groups.map((g) => {
          const pct = g.quotaBytes ? Math.min(100, g.trafficUsed / g.quotaBytes * 100) : 100;
          const tone = g.quotaBytes ? pct > 90 ? 'var(--p-warning)' : 'var(--p-primary)' : 'color-mix(in srgb,#9254de 55%,transparent)';
          return <PanelCard className="groups-card" key={g.name} title={g.name} icon="groups" pad={0} extra={<Space size={4}>
            <Button type="text" size="small" aria-label={`${t('groupParity.edit', { defaultValue: 'Edit group' })}: ${g.name}`} icon={<EditOutlined />} onClick={() => openRename(g)} />
            <Dropdown trigger={['click']} menu={{ items: rowActions(g) }}><Button type="text" size="small" aria-label={`${t('more')}: ${g.name}`} icon={<MoreOutlined />} /></Dropdown>
          </Space>}>
            <div className="groups-card-usage"><div className="groups-card-total"><span><span className="p-stat">{SizeFormatter.sizeFormat(g.trafficUsed)}</span>
              <span className="p-stat-u">{g.quotaBytes ? ` ${t('panel.of')} ${SizeFormatter.sizeFormat(g.quotaBytes)}` : ` · ${t('panel.noCap')}`}</span></span>
              {!!g.quotaBytes && <span className="p-sub">{Math.round(pct)}%</span>}</div>
              <Meter pct={pct} tone={tone} height={5} />
              <div className="groups-card-history"><ClientTrendCell wide client={{ email: g.name, history: g.history }} hint={t('telemetry.currentMemberHistory')} /></div>
            </div>
            <div className="groups-card-inbounds">{(g.sharedInboundIds ?? []).map((id) => <Tag key={id} color={inboundTone(inbounds.find((ib) => ib.id === id)?.protocol)}>{inbounds.find((ib) => ib.id === id)?.remark || inbounds.find((ib) => ib.id === id)?.tag || `#${id}`}</Tag>)}</div>
            <div className="groups-card-footer"><div className="p-kv"><span className="p-kv-k">{t('pages.groups.clientCount')}</span><span className="p-kv-v">{g.clientCount} · {g.onlineCount ?? '—'} {t('online')}</span></div>
              <div className="p-kv"><span className="p-kv-k">{t('groupParity.expiring', { defaultValue: 'Expiring soon' })}</span><span className="p-kv-v">{g.expiringCount ?? '—'}</span></div></div>
          </PanelCard>;
        })}{fetched && !groups.length && <div className="groups-empty">{t('noData')}</div>}</div>}
    </Spin>
    {formOpen && <GroupFormModal group={editing} groups={groups} inbounds={inbounds} onClose={() => setFormOpen(false)} onSaved={invalidate} />}
    {quotaGroup && <GroupQuotaModal group={quotaGroup} inbounds={inbounds} onClose={() => setQuotaGroup(null)} onSaved={invalidate} />}
    <Modal open={!!detailGroup} className="groups-parity-modal" title={detailGroup?.name} onCancel={() => setDetailGroup(null)} footer={<Button onClick={() => setDetailGroup(null)}>{t('close')}</Button>}>
      {detailGroup && <>
        <div className="groups-detail-grid"><Field label={t('pages.groups.upload')} value={SizeFormatter.sizeFormat(detailGroup.up)} /><Field label={t('pages.groups.download')} value={SizeFormatter.sizeFormat(detailGroup.down)} />
          <Field label={t('pages.groups.trafficUsed')} value={SizeFormatter.sizeFormat(detailGroup.trafficUsed)} /><Field label={t('telemetry.groupQuota')} value={detailGroup.quotaBytes ? SizeFormatter.sizeFormat(detailGroup.quotaBytes) : '∞'} /></div>
        <div className="groups-detail-section"><div className="p-kicker">{t('plans.inbounds')}</div><Space wrap size={4}>{(detailGroup.inboundIds ?? []).map((id) => <Tag key={id} color={inboundTone(inbounds.find((ib) => ib.id === id)?.protocol)}>{inbounds.find((ib) => ib.id === id)?.remark || `#${id}`}</Tag>)}</Space></div>
        <div className="groups-detail-section"><div className="p-kicker">{t('telemetry.sharedInbounds')}</div><Space wrap size={4}>{(detailGroup.sharedInboundIds ?? []).map((id) => <Tag key={id} color={inboundTone(inbounds.find((ib) => ib.id === id)?.protocol)}>{inbounds.find((ib) => ib.id === id)?.remark || `#${id}`}</Tag>)}</Space></div>
        <div className="groups-detail-section"><div className="p-kicker">{t('groupParity.membership', { defaultValue: 'Membership' })}</div>
        <Space wrap><Button icon={<UsergroupAddOutlined />} onClick={() => { openAddClientsFor(detailGroup); setDetailGroup(null); }}>{t('pages.groups.addToGroup')}</Button>
          <Button disabled={!detailGroup.clientCount} icon={<UsergroupDeleteOutlined />} onClick={() => { openRemoveClientsFor(detailGroup); setDetailGroup(null); }}>{t('pages.groups.removeFromGroup')}</Button>
          <Button onClick={() => { openRename(detailGroup); setDetailGroup(null); }}>{t('groupParity.edit', { defaultValue: 'Edit group' })}</Button></Space></div>
      </>}
    </Modal>
        <LazyMount when={subLinksOpen}>
          <SubLinksModal
            open={subLinksOpen}
            emails={groupEmails}
            clients={allClients}
            subSettings={subSettings}
            onOpenChange={setSubLinksOpen}
          />
        </LazyMount>

        <LazyMount when={adjustOpen}>
          <ClientBulkAdjustModal
            open={adjustOpen}
            count={groupEmails.length}
            onOpenChange={setAdjustOpen}
            onSubmit={async (addDays, addBytes) => {
              const msg = await bulkAdjust(groupEmails, addDays, addBytes);
              if (msg?.success) {
                const obj = msg.obj ?? { adjusted: 0 };
                messageApi.success(
                  t('pages.groups.adjustSuccess', {
                    count: obj.adjusted ?? 0,
                    name: groupForAction?.name ?? '',
                  }),
                );
                return obj;
              }
              return null;
            }}
          />
        </LazyMount>

        <LazyMount when={addClientsOpen}>
          <GroupAddClientsModal
            open={addClientsOpen}
            groupName={groupForAction?.name ?? null}
            candidates={allClients.filter((c) => c.group !== groupForAction?.name)}
            onClose={() => setAddClientsOpen(false)}
            onSubmit={async (emails) => {
              const msg = await bulkAddToGroup(emails, groupForAction?.name ?? '');
              if (msg?.success) {
                return (msg.obj as { affected?: number } | undefined) ?? { affected: 0 };
              }
              return null;
            }}
          />
        </LazyMount>

        <LazyMount when={removeClientsOpen}>
          <GroupRemoveClientsModal
            open={removeClientsOpen}
            groupName={groupForAction?.name ?? null}
            members={allClients.filter((c) => c.group === groupForAction?.name)}
            onClose={() => setRemoveClientsOpen(false)}
            onSubmit={async (emails) => {
              const msg = await bulkRemoveFromGroup(emails);
              if (msg?.success) {
                return (msg.obj as { affected?: number } | undefined) ?? { affected: 0 };
              }
              return null;
            }}
          />
        </LazyMount>
  </div>;
}
