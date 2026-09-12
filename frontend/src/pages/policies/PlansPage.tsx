import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Input, Space, Tag } from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { usePoliciesQuery } from '@/api/queries/usePoliciesQuery';
import { usePolicyMutations } from '@/api/queries/usePolicyMutations';
import { useCoresQuery } from '@/api/queries/useCoresQuery';
import { shapeableKinds } from '@/lib/cores/client-shaping';
import { describeTier } from '@/lib/policies/labels';
import { parseTiers, type PolicyRecord } from '@/schemas/api/policy';
import { PageHeader, PanelCard, Field, Meter } from '@/components/ui/panel';
import { ConfirmDelete } from '@/components/feedback';
import { SizeFormatter } from '@/utils';
import PolicyFormModal from './PolicyFormModal';

export default function PlansPage() {
  const { t } = useTranslation();
  const query = usePoliciesQuery();
  const cores = useCoresQuery();
  const { remove, saving } = usePolicyMutations();
  const [editing, setEditing] = useState<PolicyRecord | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [deleting, setDeleting] = useState<PolicyRecord | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [search, setSearch] = useState('');
  const rows = useMemo(() => query.data ?? [], [query.data]);
  const visible = rows.filter((p) => p.name.toLowerCase().includes(search.toLowerCase()));
  const selected = visible.find((p) => p.id === selectedId) ?? visible[0];
  const shapeable = shapeableKinds(cores.data ?? []);
  const tiers = selected ? parseTiers(selected.tiers) : [];
  const highest = Math.max(1, ...tiers.map((tier) => tier.fromBytes));
  function openForm(policy: PolicyRecord | null) { setEditing(policy); setFormOpen(true); }
  const limit = (value: number | null | undefined, format: (n: number) => string = String) =>
    value == null ? t('design.inherit') : value === 0 ? t('pages.policies.unlimited') : format(value);

  return <>
    <PageHeader title={t('pages.policies.title')} sub={t('pages.policies.desc')} actions={<Space>
      <Button icon={<ReloadOutlined />} loading={query.isFetching} onClick={() => { void query.refetch(); }}>{t('refresh')}</Button>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openForm(null)}>{t('pages.policies.add')}</Button>
    </Space>} />
    {query.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} description={query.error.message} />}
    <div className="p-plans">
      <PanelCard className="p-plans-rail" pad={0}>
        <div style={{ padding: 12 }}><Input.Search value={search} onChange={(e) => setSearch(e.target.value)} allowClear
          placeholder={t('design.searchPlans')} aria-label={t('design.searchPlans')} /></div>
        <div className="p-plan-list" aria-label={t('menu.policies')}>
          {visible.map((p) => <button type="button" key={p.id} className={`p-planrow${selected?.id === p.id ? ' is-active' : ''}`}
            aria-pressed={selected?.id === p.id} onClick={() => setSelectedId(p.id)}>
            <div className="p-name">{p.name}</div>
            <div className="p-sub" style={{ marginTop: 4 }}>{limit(p.quotaBytes, SizeFormatter.sizeFormat)} · {limit(p.durationDays, (n) => t('design.days', { count: n }))}</div>
          </button>)}
          {!visible.length && <div className="p-plan-empty">{query.isPending ? t('loading') : t('pages.policies.empty')}</div>}
        </div>
      </PanelCard>
      {selected && <div className="p-plan-detail">
        <PanelCard pad={0}>
          <div className="p-plandetail-h">
            <div><h2 className="p-h1">{selected.name}</h2><span className="p-sub p-mono">#{selected.id}</span></div>
            <Space><Button icon={<EditOutlined />} onClick={() => openForm(selected)}>{t('pages.policies.edit')}</Button>
              <Button danger icon={<DeleteOutlined />} onClick={() => setDeleting(selected)}>{t('remove')}</Button></Space>
          </div>
          <div className="p-planspec">
            <div className="p-planspec-c"><Field label={t('pages.clients.traffic')} value={limit(selected.quotaBytes, SizeFormatter.sizeFormat)} /></div>
            <div className="p-planspec-c"><Field label={t('pages.clients.duration')} value={limit(selected.durationDays, (n) => t('design.days', { count: n }))} /></div>
            <div className="p-planspec-c"><Field label={t('design.devices')} value={limit(selected.limitIp)} /></div>
          </div>
        </PanelCard>
        <PanelCard title={t('pages.policies.ladder')} icon="speed" pad={14}>
          {!tiers.length ? <p className="p-sub">{t('pages.policies.noTiersHint')}</p> : <>
            {tiers[0].fromBytes > 0 && <p className="p-sub">{t('pages.policies.belowFirstHint')}</p>}
            {tiers.map((tier) => <div key={tier.fromBytes} style={{ marginBottom: 14 }}>
              <div className="p-kv"><span className="p-kv-v" dir="ltr">{describeTier(tier, t)}</span></div>
              <Meter pct={tier.fromBytes / highest * 100} tone="var(--p-primary)" height={4} />
            </div>)}
          </>}
        </PanelCard>
        {!cores.isPending && !cores.isError && <Alert type={shapeable.length ? 'info' : 'warning'} showIcon
          title={shapeable.length ? t('pages.policies.ratesReach') : t('pages.policies.ratesReachNothing')}
          description={<Space size={4} wrap>{shapeable.map((kind) => <Tag key={kind}><span dir="ltr">{kind}</span></Tag>)}</Space>} />}
        {cores.isError && <Alert type="error" showIcon title={t('somethingWentWrong')} description={cores.error.message} />}
      </div>}
    </div>
    <PolicyFormModal open={formOpen} policy={editing} onClose={() => setFormOpen(false)} />
    <ConfirmDelete open={!!deleting} title={t('pages.policies.deleteConfirm')} what={deleting?.name ?? ''}
      lines={[t('design.planDeleteImpact')]} confirmLoading={saving} onCancel={() => setDeleting(null)}
      onConfirm={async () => { if (deleting && (await remove(deleting.id))?.success) setDeleting(null); }} />
  </>;
}
