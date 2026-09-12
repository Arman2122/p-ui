import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { HttpUtil, Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { markLocalInvalidate } from '@/api/invalidationTracker';
import {
  ClientHydrateSchema,
  ClientCreateRequestSchema,
  ClientPageResponseSchema,
  InboundOptionsSchema,
  OnlinesSchema,
  BulkAdjustResultSchema,
  BulkAttachResultSchema,
  BulkCreateResultSchema,
  BulkDeleteResultSchema,
  BulkSetEnableResultSchema,
  BulkResetTrafficResultSchema,
  BulkDetachResultSchema,
  BulkGroupResultSchema,
  BulkSetResultSchema,
  BulkMoveResultSchema,
  BulkClearIpsResultSchema,
  ClientLinksRowsSchema,
  BulkRotateResultSchema,
  DelDepletedResultSchema,
  type ClientHydrate,
  type ClientRecord,
  type ClientTraffic,
  type ClientsSummary,
  type ClientPageResponse,
  type InboundOption,
  type ExternalLink,
  type BulkAdjustResult,
  type BulkAttachResult,
  type BulkCreateResult,
  type BulkDeleteResult,
  type BulkSetEnableResult,
  type BulkResetTrafficResult,
  type BulkSetResult,
  type BulkMoveResult,
  type BulkClearIpsResult,
  type ClientLinksRow,
  type BulkRotateResult,
  type BulkDetachResult,
  type BulkGroupResult,
} from '@/schemas/client';
import { isEmptySelection, selectionBody, type BulkSelection } from '@/lib/clients/bulk-selection';
import type { BulkSetPatch } from '@/lib/clients/bulk-set';
import { DefaultsPayloadSchema } from '@/schemas/defaults';
import { TRAFFIC_POLL_INTERVAL_S } from '@/lib/traffic/poll-interval';

// One row sent to POST /clients/:email/externalLinks.
export type ExternalLinkInput = { kind: 'link' | 'subscription'; value: string; remark: string };

export type { ClientRecord, ClientTraffic, ClientsSummary, InboundOption, ExternalLink };

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

interface SubSettings {
  enable: boolean;
  subURI: string;
  subJsonURI: string;
  subJsonEnable: boolean;
  subClashURI: string;
  subClashEnable: boolean;
  publicHost: string;
}

export interface ClientQueryParams {
  page: number;
  pageSize: number;
  search?: string;
  // CSV strings — frontend joins arrays on ',', backend splits the same way.
  filter?: string;
  protocol?: string;
  inbound?: string;
  sort?: string;
  order?: 'ascend' | 'descend';
  expiryFrom?: number;
  expiryTo?: number;
  usageFrom?: number;
  usageTo?: number;
  autoRenew?: 'on' | 'off' | '';
  hasTgId?: 'yes' | 'no' | '';
  hasComment?: 'yes' | 'no' | '';
  group?: string;
}

const DEFAULT_QUERY: ClientQueryParams = { page: 1, pageSize: 25 };
const DEFAULT_SUMMARY: ClientsSummary = {
  total: 0, active: 0,
  onlineCount: 0, depletedCount: 0, expiringCount: 0, deactiveCount: 0,
  online: [], depleted: [], expiring: [], deactive: [],
};

export interface ClientSpeedEntry {
  up: number;
  down: number;
}

type ClientStatRow = ClientTraffic & { email?: string };

// Mirror of the server's buildClientsSummary (web/service/client.go). The
// client_stats WS event already carries every client's traffic, so the
// summary card can be recomputed live from it instead of waiting for a list
// refetch — keep the two in lockstep.
export function computeClientsSummary(
  stats: ClientStatRow[],
  onlineSet: Set<string>,
  expireDiffMs: number,
  trafficDiffBytes: number,
): ClientsSummary {
  const now = Date.now();
  const online: string[] = [];
  const depleted: string[] = [];
  const expiring: string[] = [];
  const deactive: string[] = [];
  let active = 0;
  for (const c of stats) {
    const email = c.email;
    if (!email) continue;
    const used = (c.up || 0) + (c.down || 0);
    const total = c.total || 0;
    const exhausted = total > 0 && used >= total;
    const expired = (c.expiryTime || 0) > 0 && (c.expiryTime || 0) <= now;
    if (c.enable && onlineSet.has(email)) online.push(email);
    if (exhausted || expired) { depleted.push(email); continue; }
    if (!c.enable) { deactive.push(email); continue; }
    const nearExpiry = (c.expiryTime || 0) > 0 && (c.expiryTime || 0) - now < expireDiffMs;
    const nearLimit = total > 0 && total - used < trafficDiffBytes;
    if (nearExpiry || nearLimit) expiring.push(email);
    else active += 1;
  }
  return {
    total: stats.length,
    active,
    onlineCount: online.length,
    depletedCount: depleted.length,
    expiringCount: expiring.length,
    deactiveCount: deactive.length,
    online,
    depleted,
    expiring,
    deactive,
  };
}

export function sameSpeedMap(
  a: Record<string, ClientSpeedEntry>,
  b: Record<string, ClientSpeedEntry>,
): boolean {
  const aKeys = Object.keys(a);
  if (aKeys.length !== Object.keys(b).length) return false;
  for (const key of aKeys) {
    const left = a[key];
    const right = b[key];
    if (!right || left.up !== right.up || left.down !== right.down) return false;
  }
  return true;
}

// The field list computeClientsSummary reads, and deliberately nothing else.
// lastOnline in particular churns for every online client on every push and no
// counter depends on it, so including it here would defeat the comparison.
export function sameSummaryInputs(a: ClientStatRow[], b: ClientStatRow[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const left = a[i];
    const right = b[i];
    if (left.email !== right.email
      || left.up !== right.up
      || left.down !== right.down
      || left.total !== right.total
      || left.enable !== right.enable
      || left.expiryTime !== right.expiryTime) return false;
  }
  return true;
}

export function pickClientsSummary(
  serverSummary: ClientsSummary,
  allClientStats: ClientStatRow[],
  onlineSet: Set<string>,
  expireDiffMs: number,
  trafficDiffBytes: number,
): ClientsSummary {
  if (allClientStats.length === 0) return serverSummary;
  if (serverSummary.total > allClientStats.length) return serverSummary;
  const live = computeClientsSummary(allClientStats, onlineSet, expireDiffMs, trafficDiffBytes);
  return { ...live, total: serverSummary.total || live.total };
}

function buildQS(p: ClientQueryParams): string {
  const sp = new URLSearchParams();
  sp.set('page', String(p.page || 1));
  sp.set('pageSize', String(p.pageSize || DEFAULT_QUERY.pageSize));
  if (p.search) sp.set('search', p.search);
  if (p.filter) sp.set('filter', p.filter);
  if (p.protocol) sp.set('protocol', p.protocol);
  if (p.inbound) sp.set('inbound', p.inbound);
  if (p.sort) sp.set('sort', p.sort);
  if (p.order) sp.set('order', p.order);
  if (p.expiryFrom && p.expiryFrom > 0) sp.set('expiryFrom', String(p.expiryFrom));
  if (p.expiryTo && p.expiryTo > 0) sp.set('expiryTo', String(p.expiryTo));
  if (p.usageFrom && p.usageFrom > 0) sp.set('usageFrom', String(p.usageFrom));
  if (p.usageTo && p.usageTo > 0) sp.set('usageTo', String(p.usageTo));
  if (p.autoRenew) sp.set('autoRenew', p.autoRenew);
  if (p.hasTgId) sp.set('hasTgId', p.hasTgId);
  if (p.hasComment) sp.set('hasComment', p.hasComment);
  if (p.group) sp.set('group', p.group);
  return sp.toString();
}

async function fetchClientPage(params: ClientQueryParams): Promise<ClientPageResponse> {
  const qs = buildQS(params);
  const msg = await HttpUtil.get(`/panel/api/clients/list/paged?${qs}`, undefined, { silent: true });
  if (!msg?.success || !msg.obj) throw new Error(msg?.msg || 'Failed to fetch clients');
  const validated = parseMsg(msg, ClientPageResponseSchema, 'clients/list/paged', { strict: true });
  if (!validated.obj) throw new Error('Empty clients response');
  return validated.obj;
}

async function fetchInboundOptions(): Promise<InboundOption[]> {
  const msg = await HttpUtil.get('/panel/api/inbounds/options', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch inbound options');
  const validated = parseMsg(msg, InboundOptionsSchema, 'inbounds/options');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

async function fetchDefaults(): Promise<Record<string, unknown>> {
  const msg = await HttpUtil.post('/panel/api/setting/defaultSettings', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch defaults');
  const validated = parseMsg(msg, DefaultsPayloadSchema, 'setting/defaultSettings');
  return validated.obj || {};
}

export interface UseClientsOptions {
  // Callers that only need the mutations — the bulk modals, the groups page —
  // pass false. Mounting them used to start a second 5-second poll of the paged
  // list whose result they never read, which on a large panel means a full
  // summary aggregate every 5 seconds for nothing.
  list?: boolean;
}

export function useClients(options: UseClientsOptions = {}) {
  const withList = options.list ?? true;
  const queryClient = useQueryClient();

  // Null until the page has settled on a query. The clients page cannot build
  // one until the persisted sort and the panel's configured page size are both
  // known, and fetching before then cost three sequential requests per load —
  // the first two thrown away (#trace).
  const [query, setQueryState] = useState<ClientQueryParams | null>(null);
  // setQuery shallow-compares so callers can pass a fresh object every render
  // (the common React pattern) without triggering a re-fetch when nothing
  // actually changed.
  const setQuery = useCallback((next: ClientQueryParams) => {
    setQueryState((prev) => {
      if (
        prev
        && prev.page === next.page
        && prev.pageSize === next.pageSize
        && (prev.search ?? '') === (next.search ?? '')
        && (prev.filter ?? '') === (next.filter ?? '')
        && (prev.protocol ?? '') === (next.protocol ?? '')
        && (prev.inbound ?? '') === (next.inbound ?? '')
        && (prev.sort ?? '') === (next.sort ?? '')
        && (prev.order ?? '') === (next.order ?? '')
        && (prev.expiryFrom ?? 0) === (next.expiryFrom ?? 0)
        && (prev.expiryTo ?? 0) === (next.expiryTo ?? 0)
        && (prev.usageFrom ?? 0) === (next.usageFrom ?? 0)
        && (prev.usageTo ?? 0) === (next.usageTo ?? 0)
        && (prev.autoRenew ?? '') === (next.autoRenew ?? '')
        && (prev.hasTgId ?? '') === (next.hasTgId ?? '')
        && (prev.hasComment ?? '') === (next.hasComment ?? '')
        && (prev.group ?? '') === (next.group ?? '')
      ) return prev;
      return next;
    });
  }, []);

  const listQuery = useQuery({
    queryKey: keys.clients.list(query ?? DEFAULT_QUERY),
    queryFn: () => fetchClientPage(query ?? DEFAULT_QUERY),
    enabled: withList && query !== null,
    staleTime: Infinity,
    // List is sorted/paged server-side, so the WS patch can't add new or
    // re-sort rows; poll the current page to keep it live (pauses when hidden).
    refetchInterval: 5000,
    placeholderData: keepPreviousData,
  });

  const inboundOptionsQuery = useQuery({
    queryKey: keys.inbounds.options(),
    queryFn: fetchInboundOptions,
    enabled: withList,
    staleTime: Infinity,
  });

  const defaultsQuery = useQuery({
    queryKey: keys.settings.defaults(),
    queryFn: fetchDefaults,
    staleTime: Infinity,
  });

  const onlinesQuery = useQuery({
    queryKey: keys.clients.onlines(),
    queryFn: async () => {
      const msg = await HttpUtil.post('/panel/api/clients/onlines', undefined, { silent: true });
      if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch onlines');
      const validated = parseMsg(msg, OnlinesSchema, 'clients/onlines');
      return Array.isArray(validated.obj) ? validated.obj : [];
    },
    enabled: withList,
    staleTime: Infinity,
  });

  const clients = listQuery.data?.items ?? [];
  const total = listQuery.data?.total ?? 0;
  const filtered = listQuery.data?.filtered ?? 0;
  const allGroups = listQuery.data?.groups ?? [];
  const fetched = listQuery.data !== undefined || listQuery.isError;
  const fetchError = listQuery.error ? (listQuery.error as Error).message : '';
  // isFetching is deliberately NOT read here. Touching it makes it a tracked
  // property, so the 5s refetchInterval notifies twice per cycle — two whole
  // page renders even when structural sharing leaves the data identical, and
  // each one bumps rc-table's immutable mark and re-runs every cell renderer.
  // Callers that want a spinner for an explicit refresh drive it locally.
  // Showing kept-previous data for a new key (filter/sort/page) — drives the
  // table overlay so the 5s background poll doesn't flash it.
  const transitioning = listQuery.isPlaceholderData;

  const inbounds = inboundOptionsQuery.data ?? [];
  const onlines = useMemo(() => onlinesQuery.data ?? [], [onlinesQuery.data]);

  const defaults = defaultsQuery.data ?? {};
  const subSettings: SubSettings = useMemo(() => ({
    enable: !!defaults.subEnable,
    subURI: (defaults.subURI as string) || '',
    subJsonURI: (defaults.subJsonURI as string) || '',
    subJsonEnable: !!defaults.subJsonEnable,
    subClashURI: (defaults.subClashURI as string) || '',
    subClashEnable: !!defaults.subClashEnable,
    publicHost: (defaults.subDomain as string) || (defaults.webDomain as string) || '',
  }), [
    defaults.subEnable,
    defaults.subURI,
    defaults.subJsonURI,
    defaults.subJsonEnable,
    defaults.subClashURI,
    defaults.subClashEnable,
    defaults.subDomain,
    defaults.webDomain,
  ]);

  const ipLimitEnable = !!defaults.ipLimitEnable;
  const tgBotEnable = !!defaults.tgBotEnable;
  const expireDiff = ((defaults.expireDiff as number) ?? 0) * 86400000;
  const trafficDiff = ((defaults.trafficDiff as number) ?? 0) * 1073741824;
  const pageSize = (defaults.pageSize as number) ?? 0;
  // pageSize 0 means "one long page", which is indistinguishable from "the
  // settings have not arrived yet" — so callers need this flag to know when the
  // configured page size is real. isFetched (not isSuccess) so a failed
  // settings request still lets the page fall back and render.
  const settingsReady = defaultsQuery.isFetched;

  const [allClientStats, setAllClientStats] = useState<ClientStatRow[]>([]);
  const [clientSpeed, setClientSpeed] = useState<Record<string, ClientSpeedEntry>>({});
  const summary = useMemo<ClientsSummary>(
    () => pickClientsSummary(listQuery.data?.summary ?? DEFAULT_SUMMARY, allClientStats, new Set(onlines), expireDiff, trafficDiff),
    [allClientStats, onlines, expireDiff, trafficDiff, listQuery.data?.summary],
  );

  const invalidateAll = useCallback(
    () => {
      markLocalInvalidate();
      setAllClientStats([]);
      return Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.clients.root() }),
        queryClient.invalidateQueries({ queryKey: keys.inbounds.root() }),
        queryClient.invalidateQueries({ queryKey: keys.xray.config() }),
      ]);
    },
    [queryClient],
  );

  const refresh = useCallback(async () => {
    await invalidateAll();
  }, [invalidateAll]);

  const hydrate = useCallback(async (email: string): Promise<ClientHydrate | null> => {
    if (!email) return null;
    const msg = await HttpUtil.get(`/panel/api/clients/get/${encodeURIComponent(email)}`);
    if (!msg?.success || !msg.obj) return null;
    const validated = parseMsg(msg, ClientHydrateSchema, 'clients/get');
    return validated.obj;
  }, []);

  const createMut = useMutation({
    mutationFn: (payload: unknown) =>
      HttpUtil.post('/panel/api/clients/add', ClientCreateRequestSchema.parse(payload), JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkAddToGroupMut = useMutation({
    mutationFn: async (body: { selection: BulkSelection; group: string }): Promise<Msg<BulkGroupResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/groups/bulkAdd', { ...selectionBody(body.selection), group: body.group }, JSON_HEADERS);
      return parseMsg(raw, BulkGroupResultSchema, 'clients/groups/bulkAdd');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkRemoveFromGroupMut = useMutation({
    mutationFn: async (body: { selection: BulkSelection }): Promise<Msg<BulkGroupResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/groups/bulkRemove', selectionBody(body.selection), JSON_HEADERS);
      return parseMsg(raw, BulkGroupResultSchema, 'clients/groups/bulkRemove');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const updateMut = useMutation({
    mutationFn: ({ email, client }: { email: string; client: unknown }) =>
      HttpUtil.post(`/panel/api/clients/update/${encodeURIComponent(email)}`, client, JSON_HEADERS),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const removeMut = useMutation({
    mutationFn: ({ email, keepTraffic }: { email: string; keepTraffic?: boolean }) => {
      const url = keepTraffic
        ? `/panel/api/clients/del/${encodeURIComponent(email)}?keepTraffic=1`
        : `/panel/api/clients/del/${encodeURIComponent(email)}`;
      return HttpUtil.post(url);
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkDeleteMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; keepTraffic?: boolean }): Promise<Msg<BulkDeleteResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkDel', { ...selectionBody(payload.selection), keepTraffic: payload.keepTraffic }, JSON_HEADERS);
      return parseMsg(raw, BulkDeleteResultSchema, 'clients/bulkDel');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkCreateMut = useMutation({
    mutationFn: async (payloads: unknown[]): Promise<Msg<BulkCreateResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkCreate', payloads.map((payload) => ClientCreateRequestSchema.parse(payload)), JSON_HEADERS);
      return parseMsg(raw, BulkCreateResultSchema, 'clients/bulkCreate');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkAdjustMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; addDays: number; addBytes: number; flow: string; mode: string }): Promise<Msg<BulkAdjustResult>> => {
      const { selection, ...rest } = payload;
      const raw = await HttpUtil.post('/panel/api/clients/bulkAdjust', { ...selectionBody(selection), ...rest }, JSON_HEADERS);
      return parseMsg(raw, BulkAdjustResultSchema, 'clients/bulkAdjust');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkSetMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; patch: BulkSetPatch }): Promise<Msg<BulkSetResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkSet', { ...selectionBody(payload.selection), ...payload.patch }, JSON_HEADERS);
      return parseMsg(raw, BulkSetResultSchema, 'clients/bulkSet');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkMoveMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; fromInboundIds: number[]; toInboundIds: number[] }): Promise<Msg<BulkMoveResult>> => {
      const { selection, ...rest } = payload;
      const raw = await HttpUtil.post('/panel/api/clients/bulkMove', { ...selectionBody(selection), ...rest }, JSON_HEADERS);
      return parseMsg(raw, BulkMoveResultSchema, 'clients/bulkMove');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkClearIpsMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection }): Promise<Msg<BulkClearIpsResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkClearIps', selectionBody(payload.selection), JSON_HEADERS);
      return parseMsg(raw, BulkClearIpsResultSchema, 'clients/bulkClearIps');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkRotateMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; credentials: boolean; subId: boolean }): Promise<Msg<BulkRotateResult>> => {
      const { selection, ...rest } = payload;
      const raw = await HttpUtil.post('/panel/api/clients/bulkRotate', { ...selectionBody(selection), ...rest }, JSON_HEADERS);
      return parseMsg(raw, BulkRotateResultSchema, 'clients/bulkRotate');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkSetEnableMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; enable: boolean }): Promise<Msg<BulkSetEnableResult>> => {
      const path = payload.enable ? '/panel/api/clients/bulkEnable' : '/panel/api/clients/bulkDisable';
      const raw = await HttpUtil.post(path, selectionBody(payload.selection), JSON_HEADERS);
      return parseMsg(raw, BulkSetEnableResultSchema, payload.enable ? 'clients/bulkEnable' : 'clients/bulkDisable');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkResetTrafficMut = useMutation({
    mutationFn: async (selection: BulkSelection): Promise<Msg<BulkResetTrafficResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkResetTraffic', selectionBody(selection), JSON_HEADERS);
      return parseMsg(raw, BulkResetTrafficResultSchema, 'clients/bulkResetTraffic');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const attachMut = useMutation({
    mutationFn: ({ email, inboundIds }: { email: string; inboundIds: number[] }) =>
      HttpUtil.post(`/panel/api/clients/${encodeURIComponent(email)}/attach`, { inboundIds }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const setExternalLinksMut = useMutation({
    mutationFn: ({ email, externalLinks }: { email: string; externalLinks: ExternalLinkInput[] }) =>
      HttpUtil.post(`/panel/api/clients/${encodeURIComponent(email)}/externalLinks`, { externalLinks }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const bulkAttachMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; inboundIds: number[] }): Promise<Msg<BulkAttachResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkAttach', { ...selectionBody(payload.selection), inboundIds: payload.inboundIds }, JSON_HEADERS);
      return parseMsg(raw, BulkAttachResultSchema, 'clients/bulkAttach');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const detachMut = useMutation({
    mutationFn: ({ email, inboundIds }: { email: string; inboundIds: number[] }) =>
      HttpUtil.post(`/panel/api/clients/${encodeURIComponent(email)}/detach`, { inboundIds }, { ...JSON_HEADERS, silentSuccess: true }),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });


  const bulkDetachMut = useMutation({
    mutationFn: async (payload: { selection: BulkSelection; inboundIds: number[] }): Promise<Msg<BulkDetachResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/bulkDetach', { ...selectionBody(payload.selection), inboundIds: payload.inboundIds }, JSON_HEADERS);
      return parseMsg(raw, BulkDetachResultSchema, 'clients/bulkDetach');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const resetTrafficMut = useMutation({
    mutationFn: (email: string) =>
      HttpUtil.post(`/panel/api/clients/resetTraffic/${encodeURIComponent(email)}`),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const resetAllTrafficsMut = useMutation({
    mutationFn: () => HttpUtil.post('/panel/api/clients/resetAllTraffics'),
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const delDepletedMut = useMutation({
    mutationFn: async () => {
      const raw = await HttpUtil.post('/panel/api/clients/delDepleted');
      return parseMsg(raw, DelDepletedResultSchema, 'clients/delDepleted');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const delOrphansMut = useMutation({
    mutationFn: async () => {
      const raw = await HttpUtil.post('/panel/api/clients/delOrphans');
      return parseMsg(raw, DelDepletedResultSchema, 'clients/delOrphans');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const importClientsMut = useMutation({
    mutationFn: async (data: string): Promise<Msg<BulkCreateResult>> => {
      const raw = await HttpUtil.post('/panel/api/clients/import', { data }, JSON_HEADERS);
      return parseMsg(raw, BulkCreateResultSchema, 'clients/import');
    },
    onSuccess: (msg) => { if (msg?.success) invalidateAll(); },
  });

  const create = useCallback((payload: unknown) => createMut.mutateAsync(payload), [createMut]);
  const update = useCallback((email: string, client: unknown) => {
    if (!email) return Promise.resolve(null as unknown as Msg<unknown>);
    return updateMut.mutateAsync({ email, client });
  }, [updateMut]);
  const remove = useCallback((email: string, keepTraffic = false) => {
    if (!email) return Promise.resolve(null as unknown as Msg<unknown>);
    return removeMut.mutateAsync({ email, keepTraffic });
  }, [removeMut]);
  /* Every bulk wrapper takes an email list (the callers that predate
     filter selections) or a BulkSelection naming the list filter. */
  const asSelection = (input: string[] | BulkSelection): BulkSelection => (Array.isArray(input) ? { emails: input } : input);
  const bulkDelete = useCallback((input: string[] | BulkSelection, keepTraffic = false) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkDeleteResult>);
    return bulkDeleteMut.mutateAsync({ selection, keepTraffic });
  }, [bulkDeleteMut]);
  const bulkCreate = useCallback((payloads: unknown[]) => {
    if (!Array.isArray(payloads) || payloads.length === 0) return Promise.resolve(null as unknown as Msg<BulkCreateResult>);
    return bulkCreateMut.mutateAsync(payloads);
  }, [bulkCreateMut]);
  /* mode defaults to 'add', so a caller that predates the toggle keeps the
     semantics its muscle memory expects. */
  const bulkAdjust = useCallback((input: string[] | BulkSelection, addDays: number, addBytes: number, flow = '', mode = 'add') => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null);
    return bulkAdjustMut.mutateAsync({ selection, addDays, addBytes, flow, mode });
  }, [bulkAdjustMut]);
  const bulkSet = useCallback((input: string[] | BulkSelection, patch: BulkSetPatch) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null);
    return bulkSetMut.mutateAsync({ selection, patch });
  }, [bulkSetMut]);
  const bulkMove = useCallback((input: string[] | BulkSelection, fromInboundIds: number[], toInboundIds: number[]) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null);
    return bulkMoveMut.mutateAsync({ selection, fromInboundIds, toInboundIds });
  }, [bulkMoveMut]);
  const bulkClearIps = useCallback((input: string[] | BulkSelection) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null);
    return bulkClearIpsMut.mutateAsync({ selection });
  }, [bulkClearIpsMut]);
  const bulkRotate = useCallback((input: string[] | BulkSelection, credentials: boolean, subId: boolean) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null);
    return bulkRotateMut.mutateAsync({ selection, credentials, subId });
  }, [bulkRotateMut]);
  const bulkExport = useCallback(async (input: string[] | BulkSelection): Promise<unknown[] | null> => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return null;
    const msg = await HttpUtil.post('/panel/api/clients/bulkExport', selectionBody(selection), JSON_HEADERS);
    if (!msg?.success) return null;
    return Array.isArray(msg.obj) ? msg.obj : [];
  }, []);
  const bulkLinks = useCallback(async (input: string[] | BulkSelection): Promise<ClientLinksRow[] | null> => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return null;
    const raw = await HttpUtil.post('/panel/api/clients/bulkLinks', selectionBody(selection), JSON_HEADERS);
    const msg = parseMsg(raw, ClientLinksRowsSchema, 'clients/bulkLinks');
    if (!msg?.success) return null;
    return msg.obj ?? [];
  }, []);
  const bulkEnable = useCallback((input: string[] | BulkSelection) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkSetEnableResult>);
    return bulkSetEnableMut.mutateAsync({ selection, enable: true });
  }, [bulkSetEnableMut]);
  const bulkDisable = useCallback((input: string[] | BulkSelection) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkSetEnableResult>);
    return bulkSetEnableMut.mutateAsync({ selection, enable: false });
  }, [bulkSetEnableMut]);
  const bulkResetTraffic = useCallback((input: string[] | BulkSelection) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkResetTrafficResult>);
    return bulkResetTrafficMut.mutateAsync(selection);
  }, [bulkResetTrafficMut]);
  const bulkAddToGroup = useCallback((input: string[] | BulkSelection, group: string) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkGroupResult>);
    return bulkAddToGroupMut.mutateAsync({ selection, group });
  }, [bulkAddToGroupMut]);
  const bulkRemoveFromGroup = useCallback((input: string[] | BulkSelection) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkGroupResult>);
    return bulkRemoveFromGroupMut.mutateAsync({ selection });
  }, [bulkRemoveFromGroupMut]);
  const attach = useCallback((email: string, inboundIds: number[]) => {
    if (!email) return Promise.resolve(null as unknown as Msg<unknown>);
    return attachMut.mutateAsync({ email, inboundIds });
  }, [attachMut]);
  const setExternalLinks = useCallback((email: string, externalLinks: ExternalLinkInput[]) => {
    if (!email) return Promise.resolve(null as unknown as Msg<unknown>);
    return setExternalLinksMut.mutateAsync({ email, externalLinks });
  }, [setExternalLinksMut]);
  const bulkAttach = useCallback((input: string[] | BulkSelection, inboundIds: number[]) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkAttachResult>);
    if (!Array.isArray(inboundIds) || inboundIds.length === 0) return Promise.resolve(null as unknown as Msg<BulkAttachResult>);
    return bulkAttachMut.mutateAsync({ selection, inboundIds });
  }, [bulkAttachMut]);
  const detach = useCallback((email: string, inboundIds: number[]) => {
    if (!email) return Promise.resolve(null as unknown as Msg<unknown>);
    return detachMut.mutateAsync({ email, inboundIds });
  }, [detachMut]);
  const bulkDetach = useCallback((input: string[] | BulkSelection, inboundIds: number[]) => {
    const selection = asSelection(input);
    if (isEmptySelection(selection)) return Promise.resolve(null as unknown as Msg<BulkDetachResult>);
    if (!Array.isArray(inboundIds) || inboundIds.length === 0) return Promise.resolve(null as unknown as Msg<BulkDetachResult>);
    return bulkDetachMut.mutateAsync({ selection, inboundIds });
  }, [bulkDetachMut]);
  const resetTraffic = useCallback((client: ClientRecord) => {
    if (!client?.email) return Promise.resolve(null as unknown as Msg<unknown>);
    return resetTrafficMut.mutateAsync(client.email);
  }, [resetTrafficMut]);
  const resetAllTraffics = useCallback(() => resetAllTrafficsMut.mutateAsync(), [resetAllTrafficsMut]);
  const delDepleted = useCallback(() => delDepletedMut.mutateAsync(), [delDepletedMut]);
  const delOrphans = useCallback(() => delOrphansMut.mutateAsync(), [delOrphansMut]);
  const importClients = useCallback((data: string) => importClientsMut.mutateAsync(data), [importClientsMut]);
  // Fetch the exported clients so the page can show them in a CodeMirror viewer
  // (Copy / Download), rather than triggering an immediate browser download.
  const exportClients = useCallback(async (): Promise<unknown[] | null> => {
    const msg = await HttpUtil.get('/panel/api/clients/export');
    if (!msg?.success) return null;
    return Array.isArray(msg.obj) ? msg.obj : [];
  }, []);

  /* A flip of one switch goes through the field-level endpoint, not the editor's.

     It used to rebuild a whole client payload and POST it to update, where an
     absent adTag is read as "clear it" (client_crud.go:517) while an absent
     secret is restored (:383). That asymmetry is right for the editor -- clearing
     a tag has to be possible -- and wrong for a toggle: one flip hard-deleted an
     MTProto client's ad tag on every inbound it was attached to.

     Re-sending the tag is not the fix either. ClientRecord has no adTag column,
     so the value is not on this row; and the credential map flattens every
     inbound first-write-wins, so echoing it back would stamp one inbound's tag
     onto the others -- a silent overwrite in place of a silent delete. F169. */
  const setEnable = useCallback((client: ClientRecord, enable: boolean) => {
    if (!client?.email) return Promise.resolve(null);
    return bulkSetEnableMut.mutateAsync({ selection: { emails: [client.email] }, enable });
  }, [bulkSetEnableMut]);

  // WS-driven in-place merges. Page wires these via useWebSocket; the bridge
  // covers coarse 'invalidate' and 'inbounds' events centrally.
  const queryRef = useRef(query);
  queryRef.current = query;

  const applyTrafficEvent = useCallback((payload: unknown) => {
    if (!payload || typeof payload !== 'object') return;
    const p = payload as {
      onlineClients?: string[];
      clientTraffics?: { email: string; up: number; down: number }[];
    };
    if (Array.isArray(p.onlineClients)) {
      queryClient.setQueryData(keys.clients.onlines(), p.onlineClients);
    }
    if (Array.isArray(p.clientTraffics)) {
      // Xray reports a row per client whether or not it moved a byte, so most of
      // this map used to be zeros. A missing entry and a zero entry render
      // identically (isActiveSpeed treats both as inactive), so the zeros are
      // dropped and an unchanged result returns the previous object — which lets
      // React bail out of the update instead of re-rendering the table.
      const next: Record<string, ClientSpeedEntry> = {};
      for (const ct of p.clientTraffics) {
        if (!ct || !ct.email) continue;
        const up = ct.up || 0;
        const down = ct.down || 0;
        if (up === 0 && down === 0) continue;
        next[ct.email] = {
          up: up / TRAFFIC_POLL_INTERVAL_S,
          down: down / TRAFFIC_POLL_INTERVAL_S,
        };
      }
      setClientSpeed((prev) => (sameSpeedMap(prev, next) ? prev : next));
    }
  }, [queryClient]);

  const applyClientStatsEvent = useCallback((payload: unknown) => {
    if (!payload || typeof payload !== 'object') return;
    const p = payload as { clients?: ClientStatRow[]; snapshot?: boolean };
    if (!Array.isArray(p.clients) || p.clients.length === 0) return;
    if (p.snapshot !== false) {
      const rows = p.clients;
      setAllClientStats((prev) => (sameSummaryInputs(prev, rows) ? prev : rows));
    }
    const active = queryRef.current;
    if (!active) return;
    const byEmail = new Map<string, ClientTraffic>();
    for (const row of p.clients) {
      if (row && row.email) byEmail.set(row.email, row);
    }
    queryClient.setQueryData<ClientPageResponse>(keys.clients.list(active), (prev) => {
      if (!prev) return prev;
      let touched = false;
      const next = prev.items.slice();
      for (let i = 0; i < next.length; i++) {
        const row = next[i];
        const upd = byEmail.get(row?.email);
        if (!upd) continue;
        const merged: ClientTraffic = { ...(row.traffic || {}) };
        if (typeof upd.up === 'number') merged.up = upd.up;
        if (typeof upd.down === 'number') merged.down = upd.down;
        if (typeof upd.total === 'number') merged.total = upd.total;
        if (typeof upd.expiryTime === 'number') merged.expiryTime = upd.expiryTime;
        if (typeof upd.enable === 'boolean') merged.enable = upd.enable;
        if (typeof upd.lastOnline === 'number') merged.lastOnline = upd.lastOnline;
        next[i] = { ...row, traffic: merged };
        touched = true;
      }
      if (!touched) return prev;
      return { ...prev, items: next };
    });
  }, [queryClient]);

  useEffect(() => {
    queryRef.current = query;
  }, [query]);

  return {
    clients,
    total,
    filtered,
    summary,
    allGroups,
    hydrate,
    query,
    setQuery,
    inbounds,
    onlines,
    transitioning,
    fetched,
    fetchError,
    subSettings,
    ipLimitEnable,
    tgBotEnable,
    expireDiff,
    trafficDiff,
    pageSize,
    settingsReady,
    refresh,
    create,
    bulkCreate,
    update,
    remove,
    bulkDelete,
    bulkAdjust,
    bulkSet,
    bulkMove,
    bulkClearIps,
    bulkExport,
    bulkLinks,
    bulkRotate,
    bulkEnable,
    bulkDisable,
    bulkResetTraffic,
    bulkAddToGroup,
    bulkRemoveFromGroup,
    attach,
    setExternalLinks,
    bulkAttach,
    detach,
    bulkDetach,
    resetTraffic,
    resetAllTraffics,
    delDepleted,
    delOrphans,
    exportClients,
    importClients,
    setEnable,
    clientSpeed,
    applyTrafficEvent,
    applyClientStatsEvent,
  };
}
