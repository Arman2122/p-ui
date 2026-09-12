import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Checkbox, Input, Modal, Select, Space, Table, Tag } from 'antd';
import { CopyOutlined, DeleteOutlined, DownloadOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { usePlanInboundOptions, usePlanMembersQuery, usePlanSummaryQuery, usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { shapeableKinds } from '@/lib/cores/client-shaping';
import { describeTier, formatRate } from '@/lib/policies/labels';
import { coreColor, coreIcon } from '@/lib/cores/display';
import { Icon } from '@/components/ui/Icon';
import type { CoreView } from '@/generated/zod';
import type { InboundOption } from '@/schemas/client';
import { parseTiers, PlanReapplySchema, type PolicyRecord } from '@/schemas/api/policy';
import { PageHeader, PanelCard, Field, Meter, StatusChip } from '@/components/ui/panel';
import { ConfirmDelete } from '@/components/feedback';
import { SizeFormatter } from '@/utils';
import PolicyFormModal from './PolicyFormModal';

function IncludedInbounds({ ids, inbounds, cores = [] }: { ids: number[]; inbounds: InboundOption[]; cores?: CoreView[] }) {
  const { t } = useTranslation();
  const groups = new Map<string, { label: string; core?: string; names: string[] }>();
  for (const id of ids) {
    const inbound = inbounds.find(row => row.id === id);
    const core = cores.find(row => row.kinds.includes(inbound?.protocol || ''));
    const key = core?.id || inbound?.protocol || 'unknown';
    const group = groups.get(key) || { label: core ? t(core.titleKey) : inbound?.protocol || t('plansFidelity.otherInbounds'), core: core?.id, names: [] };
    group.names.push(inbound?.remark || inbound?.tag || `#${id}`); groups.set(key, group);
  }
  return <div style={{ display: 'grid', gap: 11 }}>{[...groups].map(([key, group]) => <div key={key}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 6 }}>
      {group.core && <span style={{ color: coreColor(group.core) }}><Icon name={coreIcon(group.core)} size={13} /></span>}
      <span className="p-kicker">{group.label}</span>
    </div><Space wrap size={4}>{group.names.map(name => <Tag key={name} style={{ marginInlineEnd: 0 }}>{name}</Tag>)}</Space>
  </div>)}</div>;
}

function AdminIncludedInbounds(props: { ids: number[]; inbounds: InboundOption[] }) {
  const cores = useCoresQuery();
  return <IncludedInbounds {...props} cores={cores.data} />;
}

function RateAvailability() {
  const { t } = useTranslation();
  const cores = useCoresQuery();
  const shapeable = shapeableKinds(cores.data ?? []);
  return <>
    {!cores.isPending && !cores.isError && <Alert type={shapeable.length ? 'info' : 'warning'} showIcon
      title={shapeable.length ? t('pages.policies.ratesReach') : t('pages.policies.ratesReachNothing')}
      description={<Space size={4} wrap>{shapeable.map((kind) => <Tag key={kind}><span dir="ltr">{kind}</span></Tag>)}</Space>} />}
    {cores.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} description={cores.error.message} />}
  </>;
}

export default function PlansPage({ reseller = false }: { reseller?: boolean }) {
  const { t } = useTranslation();
  const query = usePoliciesQuery({ reseller });
  const inbounds = usePlanInboundOptions(reseller);
  const summary = usePlanSummaryQuery(reseller);
  const { remove, archive, duplicate, reapply, saving } = usePolicyMutations(reseller);
  const [editing, setEditing] = useState<PolicyRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [deleting, setDeleting] = useState<PolicyRecord | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('all');
  const [copying, setCopying] = useState<PolicyRecord | null>(null);
  const [copyName, setCopyName] = useState('');
  const [reapplying, setReapplying] = useState(false);
  const [clearOverrides, setClearOverrides] = useState(false);
  const [applyReport, setApplyReport] = useState('');
  const [page, setPage] = useState(1);
  const rows = useMemo(() => query.data ?? [], [query.data]);
  const visible = rows.filter((p) => `${p.name} ${p.description}`.toLowerCase().includes(search.toLowerCase()) && (status === 'all' || p.archived === (status === 'archived')));
  const selected = visible.find((p) => p.id === selectedId) ?? visible[0];
  const members = usePlanMembersQuery(selected?.id, page, reseller);
  const tiers = selected ? parseTiers(selected.tiers) : [];
  const initialTier = tiers.find(tier => tier.fromBytes === 0);
  const highest = Math.max(1, ...tiers.map((tier) => tier.fromBytes));
  function openForm(policy: PolicyRecord | null) { setEditing(policy); setFormOpen(true); }
  const limit = (value: number | null | undefined, format: (n: number) => string = String) =>
    value == null ? t('design.inherit') : value === 0 ? t('pages.policies.unlimited') : format(value);
  const busiest = rows.reduce<PolicyRecord | undefined>((best, row) => !best || row.assignedClients > best.assignedClients ? row : best, undefined);
  function exportPlans() {
    const url = URL.createObjectURL(new Blob([JSON.stringify(rows, null, 2)], { type: 'application/json' }));
    const link = document.createElement('a'); link.href = url; link.download = 'plans.json'; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  return <>
    <PageHeader title={t('pages.policies.title')} sub={t('pages.policies.desc')} actions={<Space wrap>
      <Button icon={<ReloadOutlined />} loading={query.isFetching} onClick={() => { void query.refetch(); }}>{t('refresh')}</Button>
      <Button icon={<DownloadOutlined />} disabled={!rows.length} onClick={exportPlans}>{t('plans.export')}</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openForm(null)}>{t('pages.policies.add')}</Button>
    </Space>} />
    {query.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} description={query.error.message} />}
    <div className="p-planspec" style={{ marginBottom: 16 }}>
      {[[t('pages.policies.title'), String(rows.length)], [t('plans.members'), String(rows.reduce((s, p) => s + (p.assignedClients || 0), 0))],
        [t('plans.withoutPlan'), summary.data ? String(summary.data.withoutPlan) : '—'],
        [t('plans.busiest'), busiest?.assignedClients ? busiest.name : '—'], [t('plans.committed'), SizeFormatter.sizeFormat(rows.reduce((s, p) => s + (p.committedBytes || 0), 0))],
      ].map(([label, value]) => <div className="p-planspec-c" key={label}><Field label={label} value={value} /></div>)}
    </div>
    {summary.isError && <Alert type="error" title={t('somethingWentWrong')} description={summary.error.message} />}
    <div className="p-plans">
      <PanelCard className="p-plans-rail" pad={0}>
        <div style={{ padding: 12 }}><Input.Search value={search} onChange={(e) => setSearch(e.target.value)} allowClear
          placeholder={t('design.searchPlans')} aria-label={t('design.searchPlans')} />
          <Select aria-label={t('plans.status')} value={status} onChange={(value) => { setStatus(value); setPage(1); }} style={{ width: '100%', marginTop: 8 }} options={[{ value: 'all', label: t('plans.all') }, { value: 'active', label: t('plans.active') }, { value: 'archived', label: t('plans.archived') }]} /></div>
        <div className="p-plan-list" aria-label={t('menu.policies')}>
          {visible.map((p) => <button type="button" key={p.id} className={`p-planrow${selected?.id === p.id ? ' is-active' : ''}`}
            aria-pressed={selected?.id === p.id} onClick={() => { setSelectedId(p.id); setPage(1); }}>
            <div className="p-name">{p.name} <span className="p-sub">{p.assignedClients || 0}</span>{p.archived && <Tag>{t('plans.archived')}</Tag>}</div>
            <div className="p-sub" style={{ marginTop: 4 }}>{limit(p.quotaBytes, SizeFormatter.sizeFormat)} · {limit(p.durationDays, (n) => t('design.days', { count: n }))}</div>
          </button>)}
          {!visible.length && <div className="p-plan-empty">{query.isPending ? t('loading') : t('pages.policies.empty')}</div>}
        </div>
      </PanelCard>
      {selected && <div className="p-plan-detail">
        <PanelCard pad={0}>
          <div className="p-plandetail-h">
            <div><h2 className="p-h1">{selected.name}</h2><StatusChip status={{ key: selected.archived ? 'archived' : 'active', label: selected.archived ? t('plans.archived') : t('plans.active'), color: selected.archived ? 'var(--p-text-2)' : 'var(--p-success)' }} /><p className="p-sub">{selected.description}</p></div>
            <Space wrap><Button type="primary" disabled={selected.archived} icon={<PlusOutlined />} href={`${(window.PUI_BASE_PATH || '/').replace(/\/$/, '')}/panel/clients?${reseller ? `new=1&plan=${selected.id}` : `planId=${selected.id}&create=1`}`}>{t('plans.newClient')}</Button>
              <Button icon={<EditOutlined />} onClick={() => openForm(selected)}>{t('pages.policies.edit')}</Button>
              <Button icon={<CopyOutlined />} onClick={() => { setCopying(selected); setCopyName(`${selected.name} ${t('plans.copySuffix')}`); }}>{t('plans.duplicate')}</Button>
              <Button loading={saving} onClick={() => { void archive(selected.id, !selected.archived); }}>{selected.archived ? t('plans.restore') : t('plans.archive')}</Button>
              <Button danger icon={<DeleteOutlined />} onClick={() => setDeleting(selected)}>{t('remove')}</Button></Space>
          </div>
          <div className="p-planspec">
            <div className="p-planspec-c"><Field label={t('pages.clients.traffic')} value={limit(selected.quotaBytes, SizeFormatter.sizeFormat)} /></div>
            <div className="p-planspec-c"><Field label={t('pages.clients.duration')} value={limit(selected.durationDays, (n) => t('design.days', { count: n }))} /></div>
            <div className="p-planspec-c"><Field label={t('plansFidelity.speed')} value={initialTier?.downBps ? `${formatRate(initialTier.downBps, t)} ↓` : t('plansFidelity.unshaped')} /></div>
            <div className="p-planspec-c"><Field label={t('design.devices')} value={limit(selected.limitIp)} /></div>
          </div>
        </PanelCard>
        <div className="p-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(min(100%,280px),1fr))' }}>
          <PanelCard kicker={t('plansFidelity.includedInbounds')} icon="inbounds" pad={14} extra={<span className="p-sub">{selected.inboundIds.length} / {inbounds.data?.length ?? '—'}</span>}>
            {reseller ? <IncludedInbounds ids={selected.inboundIds} inbounds={inbounds.data ?? []} /> : <AdminIncludedInbounds ids={selected.inboundIds} inbounds={inbounds.data ?? []} />}
            {!selected.inboundIds.length && <p className="p-sub">{t('plans.inboundsHint')}</p>}
            {inbounds.isError && <Alert type="error" title={t('somethingWentWrong')} description={inbounds.error.message} />}
          </PanelCard>
          <PanelCard kicker={t('plansFidelity.lifecycle')} icon="clock" pad={14}>
            {[
              [t('plansFidelity.windowStarts'), selected.startOnFirstUse ? t('plans.firstUse') : t('plans.startsNow')],
              [t('plansFidelity.autoRenew'), selected.autoRenewDays ? t('design.days', { count: selected.autoRenewDays }) : t('plansFidelity.off')],
              [t('plans.resetCycle'), t(`plans.${selected.resetCycle === 'none' ? 'never' : selected.resetCycle}`)],
              [t('plansFidelity.depletion'), t(`plans.${selected.onDepletion}`)],
              [t('plansFidelity.uploadCap'), initialTier?.upBps ? formatRate(initialTier.upBps, t) : t('plansFidelity.unshaped')],
              [t('plansFidelity.created'), selected.createdAt ? new Date(selected.createdAt).toLocaleDateString() : '—'],
            ].map(([label, value]) => <div className="p-kv" key={label}><span className="p-kv-k">{label}</span><span className="p-kv-v">{value}</span></div>)}
          </PanelCard>
          <PanelCard kicker={t('plansFidelity.adoption')} icon="chart" pad={14}>
            <span className="p-stat">{selected.assignedClients}</span><span className="p-stat-u">{t('menu.clients')}</span>
            <div className="p-sub" style={{ marginTop: 8 }}>{t('plans.used')}: {SizeFormatter.sizeFormat(selected.usedBytes)}</div>
            <div className="p-sub" style={{ marginTop: 4 }}>{t('plans.committed')}: {SizeFormatter.sizeFormat(selected.committedBytes)}</div>
            {!!selected.committedBytes && <div style={{ marginTop: 12 }}><Meter pct={Math.min(100, selected.usedBytes / selected.committedBytes * 100)} tone="var(--p-primary)" height={4} /></div>}
          </PanelCard>
        </div>
        <PanelCard title={t('pages.policies.ladder')} icon="speed" pad={14}>
          {!tiers.length ? <p className="p-sub">{t('pages.policies.noTiersHint')}</p> : <>
            {tiers[0].fromBytes > 0 && <p className="p-sub">{t('pages.policies.belowFirstHint')}</p>}
            {tiers.map((tier) => <div key={tier.fromBytes} style={{ marginBottom: 14 }}>
              <div className="p-kv"><span className="p-kv-v" dir="ltr">{describeTier(tier, t)}</span></div>
              <Meter pct={tier.fromBytes / highest * 100} tone="var(--p-primary)" height={4} />
            </div>)}
          </>}
        </PanelCard>
        {!reseller && <RateAvailability />}
        <PanelCard title={t('plans.members')} extra={!reseller && <Button loading={saving} onClick={() => { setClearOverrides(false); setReapplying(true); }}>{t('plans.reapply')}</Button>}>
          {!!selected.pendingClients && <Alert type="warning" title={t('plans.pendingCount', { count: selected.pendingClients })} style={{ marginBottom: 12 }} />}
          {applyReport && <Alert type="info" title={applyReport} style={{ marginBottom: 12 }} />}
          <div className="p-planspec"><Field label={t('plans.used')} value={SizeFormatter.sizeFormat(selected.usedBytes || 0)} /><Field label={t('plans.committed')} value={SizeFormatter.sizeFormat(selected.committedBytes || 0)} /></div>
          <Table rowKey="email" size="small" loading={members.isPending} dataSource={members.data?.items ?? []} scroll={{ x: 560 }}
            pagination={{ current: page, pageSize: 20, total: members.data?.total ?? 0, showSizeChanger: false, onChange: setPage }}
            columns={[
              { title: t('plans.client'), dataIndex: 'email', render: (email: string) => <span dir="ltr">{email}</span> },
              { title: t('plans.group'), dataIndex: 'group' },
              { title: t('plans.used'), key: 'usage', render: (_, member) => <><span>{SizeFormatter.sizeFormat(member.usedBytes)} / {member.quotaBytes ? SizeFormatter.sizeFormat(member.quotaBytes) : '∞'}</span><Meter pct={member.quotaBytes ? Math.min(100, member.usedBytes / member.quotaBytes * 100) : 0} /></> },
              { title: t('plans.duration'), dataIndex: 'expiryTime', render: (expiry: number) => expiry === 0 ? '∞' : expiry < 0 ? t('plans.firstUse') : new Date(expiry).toLocaleDateString() },
              { title: t('plans.status'), key: 'status', render: (_, member) => <Space direction="vertical" size={2}><Tag>{member.enable ? t('plans.active') : t('plans.disabled')}</Tag>{member.pending && <Tag color="orange">{t('plans.pending')}</Tag>}{(member.overrideQuota || member.overrideExpiry || member.overrideLimitIp || member.overrideInbounds) && <Tag>{t('plans.overridden')}</Tag>}</Space> },
            ]} />
          {members.isError && <Alert type="error" title={t('somethingWentWrong')} description={members.error.message} />}
        </PanelCard>
      </div>}
    </div>
    <PolicyFormModal open={formOpen} policy={editing} plans={rows} inbounds={inbounds.data ?? []} reseller={reseller} onClose={() => setFormOpen(false)} />
    <Modal open={!!copying} title={t('plans.duplicate')} okText={t('plans.duplicate')} okButtonProps={{ disabled: !copyName.trim() }} cancelText={t('cancel')} confirmLoading={saving} onCancel={() => setCopying(null)} onOk={async () => { if (copying && copyName.trim() && (await duplicate(copying.id, copyName.trim()))?.success) setCopying(null); }}>
      <Input value={copyName} maxLength={128} onChange={(e) => setCopyName(e.target.value)} aria-label={t('pages.policies.name')} />
    </Modal>
    <Modal open={reapplying} title={t('plans.reapply')} okText={t('plans.reapply')} okButtonProps={{ disabled: !selected }} cancelText={t('cancel')} confirmLoading={saving} onCancel={() => setReapplying(false)} onOk={async () => {
      if (!selected) return;
      const result = await reapply(selected.id, clearOverrides);
      if (!result?.success) return;
      const parsed = PlanReapplySchema.parse(result.obj);
      setApplyReport(`${t('plans.applyCount', { count: parsed.applied })}${parsed.failures.length ? ` · ${parsed.failures.map((f) => `${f.email}: ${f.reason}`).join('; ')}` : ''}`);
      setReapplying(false);
    }}><p>{t('plans.reapplyHint')}</p><Checkbox checked={clearOverrides} onChange={(e) => setClearOverrides(e.target.checked)}>{t('plans.clearOverrides')}</Checkbox></Modal>
    <ConfirmDelete open={!!deleting} title={t('pages.policies.deleteConfirm')} what={deleting?.name ?? ''}
      lines={[t('design.planDeleteImpact')]} confirmLoading={saving} onCancel={() => setDeleting(null)}
      onConfirm={async () => { if (deleting && (await remove(deleting.id))?.success) setDeleting(null); }} />
  </>;
}
