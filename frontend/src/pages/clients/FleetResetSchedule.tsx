import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Divider, Form, Popconfirm, Select, Space, Spin, Tag } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { z } from 'zod';
import { ResetScheduleCancelRequestSchema, ResetScheduleRequestSchema, ResetScheduleViewSchema, type ResetScheduleView } from '@/generated/zod';
import { useSignedInUser } from '@/pages/resellers/api';
import { HttpUtil, RandomUtil } from '@/utils';

const CycleSchema = ResetScheduleRequestSchema.shape.cycle.unwrap();
const ChoiceSchema = z.union([z.literal('inherit'), CycleSchema]);
type Choice = z.infer<typeof ChoiceSchema>;
const OperationSchema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('schedule'), request: ResetScheduleRequestSchema.strict().refine(request => request.override ? request.cycle !== undefined : request.cycle === undefined) }).strict(),
  z.object({ kind: z.literal('cancel'), request: ResetScheduleCancelRequestSchema.strict() }).strict(),
]);
type Operation = z.infer<typeof OperationSchema>;

function storedOperation(key: string, clientId: string) {
  try {
    const raw = sessionStorage.getItem(key);
    if (raw === null) return { operation: null, unavailable: false };
    const operation = OperationSchema.parse(JSON.parse(raw));
    if (operation.request.clientId !== clientId) return { operation: null, unavailable: true };
    return { operation, unavailable: false };
  } catch {
    return { operation: null, unavailable: true };
  }
}

function ScheduleChoice({ view, disabled, onSave }: { view: ResetScheduleView; disabled: boolean; onSave: (choice: Choice) => void }) {
  const { t } = useTranslation();
  const initial = view.inherited ? 'inherit' : CycleSchema.safeParse(view.cycle).data;
  const [choice, setChoice] = useState<Choice | undefined>(initial);
  return <Form layout="vertical" onFinish={() => { if (choice) onSave(choice); }}>
    <Form.Item label={t('pages.clients.resetSchedule.policy')} htmlFor={`fleet-reset-policy-${view.clientId}`}>
      <Select id={`fleet-reset-policy-${view.clientId}`} value={choice} disabled={disabled} onChange={value => setChoice(ChoiceSchema.parse(value))} options={[
        { value: 'inherit', label: t('pages.clients.resetSchedule.inherit') },
        { value: 'none', label: t('pages.clients.resetSchedule.none') },
        { value: 'hourly', label: t('pages.clients.resetSchedule.hourly') },
        { value: 'daily', label: t('pages.clients.resetSchedule.daily') },
        { value: 'weekly', label: t('pages.clients.resetSchedule.weekly') },
        { value: 'monthly', label: t('pages.clients.resetSchedule.monthly') },
      ]} />
    </Form.Item>
    <Button type="primary" htmlType="submit" disabled={disabled || !choice || choice === initial && view.revision > 0}>{t('pages.clients.resetSchedule.save')}</Button>
  </Form>;
}

function AdministratorSchedule({ actorId, clientId }: { actorId: number; clientId: string }) {
  const { t, i18n } = useTranslation();
  const storageKey = `fleet-reset-request:${actorId}:${clientId}`;
  const [initial] = useState(() => storedOperation(storageKey, clientId));
  const [operation, setOperation] = useState<Operation | null>(initial.operation);
  const [storageUnavailable, setStorageUnavailable] = useState(initial.unavailable);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<'saved' | 'cancelled' | 'reconciled' | null>(null);
  const busyRef = useRef(false);
  const path = `/panel/api/fleet/clients/${encodeURIComponent(clientId)}/reset-schedule`;
  const query = useQuery({ queryKey: ['fleet-reset-schedule', actorId, clientId], retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(path, undefined, { silent: true, signal });
      if (!response.success) throw new Error('Reset schedule unavailable');
      const view = ResetScheduleViewSchema.parse(response.obj);
      if (view.clientId !== clientId) throw new Error('Reset schedule identity mismatch');
      return view;
    },
  });
  const view = query.data;
  const fresh = query.isFetchedAfterMount && !query.isFetching && !query.isError;
  const clearRetained = useCallback((expected: Operation) => {
    const raw = sessionStorage.getItem(storageKey);
    if (raw !== null && JSON.stringify(OperationSchema.parse(JSON.parse(raw))) !== JSON.stringify(expected)) return false;
    sessionStorage.removeItem(storageKey);
    return true;
  }, [storageKey]);

  useEffect(() => {
    if (!fresh || !view || !operation || busy || view.revision <= operation.request.expectedRevision) return;
    try {
      if (!clearRetained(operation)) { setStorageUnavailable(true); return; }
      setOperation(null);
      setFeedback('reconciled');
    } catch { setStorageUnavailable(true); }
  }, [fresh, view, operation, busy, clearRetained]);

  function persist(next: Operation) {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(OperationSchema.parse(next)));
      setOperation(next);
      return true;
    } catch {
      setStorageUnavailable(true);
      return false;
    }
  }

  async function send(next: Operation) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setFeedback(null);
    try {
      const response = await HttpUtil.post(next.kind === 'cancel' ? `${path}/cancel` : path, next.request, { silent: true });
      if (!response.success) return;
      const accepted = ResetScheduleViewSchema.parse(response.obj);
      if (accepted.clientId !== clientId || accepted.revision < next.request.expectedRevision) return;
      try { if (!clearRetained(next)) { setStorageUnavailable(true); return; } } catch { setStorageUnavailable(true); return; }
      setOperation(null);
      setFeedback(next.kind === 'cancel' ? 'cancelled' : 'saved');
      await query.refetch();
    } catch {
      // The exact request remains available after an unconfirmed response.
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }

  function save(choice: Choice) {
    if (!view || !fresh || view.pendingOperationId || operation || busyRef.current || storageUnavailable) return;
    const request = ResetScheduleRequestSchema.parse({ operationId: RandomUtil.randomUUID(), clientId, expectedRevision: view.revision,
      override: choice !== 'inherit', ...(choice === 'inherit' ? {} : { cycle: choice }) });
    const next: Operation = { kind: 'schedule', request };
    if (persist(next)) void send(next);
  }

  function cancelReset() {
    if (!view?.pendingOperationId || !fresh || operation || busyRef.current || storageUnavailable) return;
    const request = ResetScheduleCancelRequestSchema.parse({ operationId: RandomUtil.randomUUID(), clientId,
      pendingOperationId: view.pendingOperationId, expectedRevision: view.revision });
    const next: Operation = { kind: 'cancel', request };
    if (persist(next)) void send(next);
  }

  function utc(value: number) {
    return value > 0 && Number.isFinite(new Date(value).valueOf()) ? new Intl.DateTimeFormat(i18n.language, { timeZone: 'UTC', year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', timeZoneName: 'short' }).format(value) : '—';
  }

  const labels = { none: t('pages.clients.resetSchedule.none'), hourly: t('pages.clients.resetSchedule.hourly'), daily: t('pages.clients.resetSchedule.daily'),
    weekly: t('pages.clients.resetSchedule.weekly'), monthly: t('pages.clients.resetSchedule.monthly') };
  const cycle = CycleSchema.safeParse(view?.cycle).data;
  const status = view?.diagnostic === 'RESET_CANCELLED' ? t('pages.clients.resetSchedule.cancelled')
    : view?.diagnostic === 'SOURCE_CHANGED' ? t('pages.clients.resetSchedule.sourceChanged')
      : view?.diagnostic === 'RECOVERY_REQUIRED' ? t('pages.clients.resetSchedule.recoveryRequired')
        : view?.pendingOperationId ? t('pages.clients.resetSchedule.pending')
          : view?.diagnostic ? t('pages.clients.resetSchedule.unavailable')
            : !view?.nextAttemptUnixMilli && cycle !== 'none' ? t('pages.clients.resetSchedule.waiting')
              : cycle === 'none' ? labels.none : t('pages.clients.resetSchedule.scheduled');
  const disabled = busy || !fresh || storageUnavailable || !!operation || !!view?.pendingOperationId;

  return <section aria-label={t('pages.clients.resetSchedule.title')}>
    <Divider>{t('pages.clients.resetSchedule.title')}</Divider>
    <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
      <span>{t('pages.clients.resetSchedule.description')}</span>
      <Button icon={<ReloadOutlined />} aria-label={t('pages.clients.resetSchedule.refresh')} loading={query.isFetching} disabled={busy} onClick={() => { setFeedback(null); void query.refetch(); }}>{t('pages.clients.resetSchedule.refresh')}</Button>
      {busy && <Space role="status"><Spin size="small" /><span>{t('pages.clients.resetSchedule.sending')}</span></Space>}
      {query.isPending && <Spin aria-label={t('loading')} />}
      {query.isError && <Alert type="error" title={t('pages.clients.resetSchedule.loadError')} />}
      {storageUnavailable && <Alert type="error" title={t('pages.clients.resetSchedule.storageUnavailable')} />}
      {feedback && <Alert type="success" title={feedback === 'saved' ? t('pages.clients.resetSchedule.saved') : feedback === 'cancelled' ? t('pages.clients.resetSchedule.cancelled') : t('pages.clients.resetSchedule.reconciled')} />}
      {operation && !busy && <Alert type="warning" title={t('pages.clients.resetSchedule.unconfirmed')} description={<Space orientation="vertical">
        <span>{operation.kind === 'cancel' ? t('pages.clients.resetSchedule.cancelPending') : `${t('pages.clients.resetSchedule.requestedPolicy')}: ${operation.request.override && operation.request.cycle ? labels[operation.request.cycle] : t('pages.clients.resetSchedule.inherit')}`}</span>
        <span>{t('pages.clients.resetSchedule.unconfirmedHint')}</span>
        <Button loading={busy} disabled={storageUnavailable || query.isFetching} onClick={() => void send(operation)}>{t('pages.clients.resetSchedule.retrySame')}</Button>
      </Space>} />}
      {view && <>
        <table className="info-table"><tbody>
          <tr><td>{t('pages.clients.resetSchedule.effective')}</td><td>{cycle ? labels[cycle] : t('pages.clients.accounting.unknown')} {view.inherited && <Tag>{t('pages.clients.resetSchedule.inherited')}</Tag>}</td></tr>
          <tr><td>{t('status')}</td><td>{status}</td></tr>
          <tr><td>{t('pages.clients.resetSchedule.anchor')}</td><td>{utc(view.anchorUnixMilli)}</td></tr>
          <tr><td>{t('pages.clients.resetSchedule.nextDue')}</td><td>{utc(view.nextDueUnixMilli)}</td></tr>
          {view.nextAttemptUnixMilli > 0 && view.nextAttemptUnixMilli !== view.nextDueUnixMilli && <tr><td>{t('pages.clients.resetSchedule.nextAttempt')}</td><td>{utc(view.nextAttemptUnixMilli)}</td></tr>}
        </tbody></table>
        <span>{t('pages.clients.resetSchedule.utcCadence')}</span>
        {view.pendingOperationId && <Alert type="warning" title={t('pages.clients.resetSchedule.pending')} description={t('pages.clients.resetSchedule.pendingHint')} />}
        <ScheduleChoice key={`${view.revision}:${view.inherited}:${view.cycle}`} view={view} disabled={disabled} onSave={save} />
        {view.pendingOperationId && <Popconfirm title={t('pages.clients.resetSchedule.cancelTitle')} description={t('pages.clients.resetSchedule.cancelHint')}
          okText={t('pages.clients.resetSchedule.confirmCancel')} cancelText={t('close')} onConfirm={cancelReset}
          disabled={busy || !fresh || !!operation || storageUnavailable}>
          <Button danger disabled={busy || !fresh || !!operation || storageUnavailable}>{t('pages.clients.resetSchedule.cancelPending')}</Button>
        </Popconfirm>}
      </>}
    </Space>
  </section>;
}

export default function FleetResetSchedule({ clientId }: { clientId: string }) {
  const user = useSignedInUser();
  if (user.isError || user.data?.role !== 'admin') return null;
  return <AdministratorSchedule key={`${user.data.id}:${clientId}`} actorId={user.data.id} clientId={clientId} />;
}
