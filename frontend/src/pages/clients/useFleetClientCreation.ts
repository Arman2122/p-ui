import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { z } from 'zod';
import { ClientCreatePayloadSchema, FleetClientCreateCapabilitiesSchema, FleetClientCreateResultSchema, type FleetClientCreateResult } from '@/generated/zod';
import { useSignedInUser } from '@/pages/resellers/api';
import { HttpUtil, Msg, RandomUtil } from '@/utils';

const RequestSchema = ClientCreatePayloadSchema.extend({ operationId: z.uuid(), expectedActorId: z.number().int().positive() });
const LocatorSchema = z.object({ actorId: z.number().int().positive(), activeOperationId: z.uuid().nullable(), operations: z.array(z.uuid()) }).strict()
  .refine(value => new Set(value.operations).size === value.operations.length && (!value.activeOperationId || value.operations.includes(value.activeOperationId)));
type Locator = z.infer<typeof LocatorSchema>;
type Captured = { actorId: number; operationId: string; body: string; lifetime: number };
type Notice = 'unavailable' | 'storage' | 'invalid' | 'uncertain' | 'receiptUnavailable' | 'saved' | null;
type State = { actorId: number; locator: Locator; unavailable: boolean; busy: boolean; memory: boolean; notice: Notice; diagnostic: string | null; result: FleetClientCreateResult | null };
const path = '/panel/api/fleet/clients';
const storageKey = (actorId: number) => `fleet-client-creation:${actorId}`;

function emptyLocator(actorId: number): Locator { return { actorId, activeOperationId: null, operations: [] }; }

function readLocator(actorId: number) {
  const raw = sessionStorage.getItem(storageKey(actorId));
  const locator = raw === null ? emptyLocator(actorId) : LocatorSchema.parse(JSON.parse(raw));
  if (locator.actorId !== actorId) throw new Error('Creation locator actor mismatch');
  return locator;
}

export function useFleetClientCreation(active: boolean, onAccepted: (result: FleetClientCreateResult) => void) {
  const actor = useSignedInUser();
  const actorId = actor.data?.role === 'admin' ? actor.data.id : 0;
  const [state, setState] = useState<State>({ actorId: 0, locator: emptyLocator(0), unavailable: true, busy: false, memory: false, notice: null, diagnostic: null, result: null });
  const current = useRef(state);
  const captured = useRef<Captured | null>(null);
  const alive = useRef(true);
  const lifetime = useRef(0);
  const activeRequest = useRef<AbortController | null>(null);
  const apply = useCallback((next: State) => { current.current = next; setState(next); }, []);
  useEffect(() => { alive.current = true; return () => {
    alive.current = false; captured.current = null; activeRequest.current?.abort();
  }; }, []);
  useEffect(() => {
    lifetime.current++;
    activeRequest.current?.abort();
    activeRequest.current = null;
    captured.current = null;
    let locator = emptyLocator(actorId), unavailable = !actorId;
    if (actorId) {
      try { locator = readLocator(actorId); } catch { unavailable = true; }
    }
    apply({ actorId, locator, unavailable, busy: false, memory: false, notice: unavailable ? 'storage' : null, diagnostic: null, result: null });
  }, [actorId, apply]);

  const capability = useQuery({ queryKey: ['fleet-client-create-capabilities', actorId], enabled: active && actorId > 0,
    retry: false, staleTime: 0, refetchOnWindowFocus: false, refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const response = await HttpUtil.get(`${path}/capabilities`, undefined, { signal, silent: true });
      if (!response.success) throw new Error('Client creation capability unavailable');
      return FleetClientCreateCapabilitiesSchema.parse(response.obj);
    },
  });
  const fresh = capability.isFetchedAfterMount && !capability.isFetching && !capability.isError;
  const available = state.actorId === actorId && actorId > 0 && capability.data?.actorId === actorId && !actor.isError && !state.unavailable && fresh;
  const mode = available && capability.data?.supported === true ? 'fleet'
    : available && capability.data?.supported === false && state.locator.operations.length === 0 ? 'legacy' : 'unavailable';

  function sameLifetime(owner: number, expected: number) {
    return alive.current && current.current.actorId === owner && lifetime.current === expected;
  }

  function persist(owner: number, change: (previous: Locator) => Locator, expectedActive?: string | null) {
    try {
      const previous = readLocator(owner);
      if (expectedActive !== undefined && previous.activeOperationId !== expectedActive) {
        if (alive.current && current.current.actorId === owner) apply({ ...current.current, locator: previous,
          memory: captured.current?.operationId === previous.activeOperationId, notice: 'uncertain' });
        return false;
      }
      const next = LocatorSchema.parse(change(previous));
      const encoded = JSON.stringify(next);
      sessionStorage.setItem(storageKey(owner), encoded);
      if (sessionStorage.getItem(storageKey(owner)) !== encoded) throw new Error('Creation locator was not retained');
      if (alive.current && current.current.actorId === owner) apply({ ...current.current, locator: next });
      return true;
    } catch {
      if (alive.current && current.current.actorId === owner) apply({ ...current.current, unavailable: true, notice: 'storage' });
      return false;
    }
  }

  function accept(owner: number, operationId: string, raw: unknown, expectedLifetime: number) {
    if (!sameLifetime(owner, expectedLifetime)) return null;
    const result = FleetClientCreateResultSchema.parse(raw);
    if (result.actorId !== owner || !z.uuid().safeParse(result.clientId).success || result.panelClientId <= 0 || result.revision <= 0) throw new Error('Invalid creation receipt');
    if (!persist(owner, previous => ({ ...previous, operations: previous.operations.filter(id => id !== operationId),
      activeOperationId: previous.activeOperationId === operationId ? null : previous.activeOperationId }))) return null;
    if (captured.current?.operationId === operationId) captured.current = null;
    apply({ ...current.current, memory: captured.current !== null, notice: 'saved', diagnostic: null, result });
    onAccepted(result);
    return result;
  }

  async function send(request: Captured) {
    if (current.current.busy || !sameLifetime(request.actorId, request.lifetime)) return new Msg(false);
    const controller = new AbortController();
    activeRequest.current = controller;
    apply({ ...current.current, busy: true, notice: null, diagnostic: null });
    try {
      const response = await HttpUtil.post(path, JSON.parse(request.body), { silent: true, headers: { 'Content-Type': 'application/json' }, timeout: 60_000, signal: controller.signal });
      if (response.success) {
        const accepted = accept(request.actorId, request.operationId, response.obj, request.lifetime);
        if (accepted) return new Msg(true, '', accepted);
      }
      if (sameLifetime(request.actorId, request.lifetime) && !current.current.unavailable) apply({ ...current.current, notice: 'uncertain', diagnostic: response.msg.slice(0, 500) || null });
      return new Msg(false);
    } catch {
      if (sameLifetime(request.actorId, request.lifetime) && !current.current.unavailable) apply({ ...current.current, notice: 'uncertain' });
      return new Msg(false);
    } finally {
      if (activeRequest.current === controller) activeRequest.current = null;
      if (sameLifetime(request.actorId, request.lifetime)) apply({ ...current.current, busy: false });
    }
  }

  async function submit(payload: unknown) {
    if (mode !== 'fleet' || current.current.busy || current.current.locator.activeOperationId || current.current.actorId !== actorId) return new Msg(false);
    const operationId = RandomUtil.randomUUID();
    const input = z.record(z.string(), z.unknown()).safeParse(payload);
    const parsed = input.success ? RequestSchema.safeParse({ ...input.data, operationId, expectedActorId: actorId }) : null;
    if (!parsed?.success) { apply({ ...current.current, notice: 'invalid' }); return new Msg(false); }
    const request = { actorId, operationId, body: JSON.stringify(parsed.data), lifetime: lifetime.current };
    if (!persist(actorId, previous => ({ ...previous, activeOperationId: operationId, operations: [...new Set([...previous.operations, operationId])] }), null)) return new Msg(false);
    captured.current = request;
    apply({ ...current.current, memory: true, result: null });
    return send(request);
  }

  async function retry() {
    const request = captured.current;
    if (mode !== 'fleet' || !request || request.actorId !== actorId || request.operationId !== current.current.locator.activeOperationId) return new Msg(false);
    return send(request);
  }

  async function checkReceipt(operationId: string) {
    const owner = actorId;
    const expectedLifetime = lifetime.current;
    if (!owner || current.current.actorId !== owner || current.current.busy || !current.current.locator.operations.includes(operationId)) return;
    const controller = new AbortController();
    activeRequest.current = controller;
    apply({ ...current.current, busy: true, notice: null, diagnostic: null });
    try {
      const response = await HttpUtil.get(`${path}/creation/${encodeURIComponent(operationId)}`, undefined, { silent: true, timeout: 30_000, signal: controller.signal });
      if (response.success) {
        const accepted = accept(owner, operationId, response.obj, expectedLifetime);
        if (accepted) return accepted;
      }
      if (sameLifetime(owner, expectedLifetime) && !current.current.unavailable) apply({ ...current.current, notice: 'receiptUnavailable', diagnostic: response.msg.slice(0, 500) || null });
    } catch {
      if (sameLifetime(owner, expectedLifetime) && !current.current.unavailable) apply({ ...current.current, notice: 'receiptUnavailable' });
    } finally {
      if (activeRequest.current === controller) activeRequest.current = null;
      if (sameLifetime(owner, expectedLifetime)) apply({ ...current.current, busy: false });
    }
  }

  function startNew() {
    const operationId = current.current.locator.activeOperationId;
    if (current.current.busy || !operationId || current.current.actorId !== actorId || mode !== 'fleet') return false;
    if (!persist(actorId, previous => ({ ...previous, activeOperationId: null }), operationId)) return false;
    captured.current = null;
    apply({ ...current.current, memory: false, notice: null, diagnostic: null, result: null });
    return true;
  }

  async function refreshCapability() {
    if (actorId) {
      try {
        const locator = readLocator(actorId);
        if (captured.current?.operationId !== locator.activeOperationId) captured.current = null;
        apply({ ...current.current, locator, memory: captured.current !== null, unavailable: false, notice: null });
      } catch { apply({ ...current.current, unavailable: true, notice: 'storage' }); }
    }
    await actor.refetch();
    if (actorId) await capability.refetch();
  }

  const ownState = state.actorId === actorId;
  return { actorId, mode, busy: ownState && state.busy, memory: ownState && state.memory, notice: ownState ? state.notice : null, diagnostic: ownState ? state.diagnostic : null, result: ownState ? state.result : null,
    activeOperationId: state.actorId === actorId ? state.locator.activeOperationId : null,
    operations: state.actorId === actorId ? state.locator.operations : [],
    loading: actor.isPending || capability.isFetching,
    submit, retry, checkReceipt, startNew, refreshCapability };
}

export type FleetClientCreation = ReturnType<typeof useFleetClientCreation>;
