import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { z } from 'zod';

import { HttpUtil, Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';
import { exitIdFromKey, isExitKey } from '@/schemas/api/egress';
import {
  OutboundTrafficListSchema,
  OutboundTestResultListSchema,
  RoutingSubjectSchema,
  XrayConfigPayloadSchema,
  XraySettingsValueSchema,
  type OutboundTestResult,
  type OutboundTrafficRow,
} from '@/schemas/xray';

type RoutingSubject = z.infer<typeof RoutingSubjectSchema>;

const DEFAULT_TEST_URL = 'https://www.google.com/generate_204';
// One HTTP-mode batch request tests this many outbounds through a single
// shared temp xray instance; chunking keeps responses bounded (~30s worst
// case — each probe is a cold plus a warm request) and lands Test All
// results progressively.
const HTTP_BATCH_CHUNK = 16;

function normalizeOutboundTestUrl(url: string) {
  return url || DEFAULT_TEST_URL;
}

export function isUdpOutbound(outbound: unknown): boolean {
  const o = outbound as { protocol?: string; streamSettings?: { network?: string } } | null | undefined;
  const p = o?.protocol;
  const n = o?.streamSettings?.network;
  return p === 'wireguard' || p === 'hysteria' || n === 'hysteria' || n === 'kcp' || n === 'quic';
}

export type OutboundTestMode = 'tcp' | 'http' | 'real';

export type { OutboundTrafficRow, OutboundTestResult };

export type XraySettingsValue = z.infer<typeof XraySettingsValueSchema>;

export interface OutboundTestState {
  testing?: boolean;
  result?: OutboundTestResult | null;
  mode?: string;
}

export type SetTemplate = (
  next: XraySettingsValue | null | ((prev: XraySettingsValue | null) => XraySettingsValue | null),
) => void;

export interface UseXraySettingResult {
  fetched: boolean;
  spinning: boolean;
  saveDisabled: boolean;
  fetchError: string;
  xraySetting: string;
  setXraySetting: (next: string) => void;
  templateSettings: XraySettingsValue | null;
  setTemplateSettings: SetTemplate;
  outboundTestUrl: string;
  setOutboundTestUrl: (v: string) => void;
  inboundTags: string[];
  routingSubjects: RoutingSubject[];
  clientReverseTags: string[];
  subscriptionOutbounds: unknown[];
  subscriptionOutboundTags: string[];
  outboundsTraffic: OutboundTrafficRow[];
  outboundTestStates: Record<number, OutboundTestState>;
  subscriptionTestStates: Record<string, OutboundTestState>;
  testingAll: boolean;
  fetchAll: () => Promise<void>;
  fetchOutboundsTraffic: () => Promise<void>;
  resetOutboundsTraffic: (tag: string) => Promise<void>;
  testOutbound: (
    index: number,
    outbound: unknown,
    mode?: string,
  ) => Promise<OutboundTestResult | null>;
  testExit: (key: number, mode?: string) => Promise<OutboundTestResult | null>;
  testSubscriptionOutbound: (
    tag: string,
    outbound: unknown,
    mode?: string,
  ) => Promise<OutboundTestResult | null>;
  testAllOutbounds: (mode?: string, exitKeys?: number[]) => Promise<void>;
  saveAll: (snapshot?: string) => Promise<void>;
  resetToDefault: () => Promise<void>;
}

type XrayConfigPayload = z.infer<typeof XrayConfigPayloadSchema>;

export async function fetchXrayConfig(): Promise<XrayConfigPayload> {
  const msg = await HttpUtil.post('/panel/api/xray/', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to load xray config');
  if (typeof msg.obj !== 'string') throw new Error('Malformed xray config response: expected string');
  let parsed: unknown;
  try {
    parsed = JSON.parse(msg.obj);
  } catch (e) {
    const err = e as Error;
    throw new Error(`Malformed xray config response: ${err.message}`, { cause: e });
  }
  const result = XrayConfigPayloadSchema.safeParse(parsed);
  if (!result.success) {
    console.warn('[zod] xray/ config payload failed validation', result.error.issues);
    return parsed as XrayConfigPayload;
  }
  return result.data;
}

async function fetchOutboundsTraffic(): Promise<OutboundTrafficRow[]> {
  const msg = await HttpUtil.get('/panel/api/xray/getOutboundsTraffic', undefined, { silent: true });
  if (!msg?.success) throw new Error(msg?.msg || 'Failed to fetch outbounds traffic');
  const validated = parseMsg(msg, OutboundTrafficListSchema, 'xray/getOutboundsTraffic');
  return Array.isArray(validated.obj) ? validated.obj : [];
}

export function useXraySetting(): UseXraySettingResult {
  const queryClient = useQueryClient();

  const configQuery = useQuery({
    queryKey: keys.xray.config(),
    queryFn: fetchXrayConfig,
    staleTime: Infinity,
  });

  const trafficQuery = useQuery({
    queryKey: keys.xray.outboundsTraffic(),
    queryFn: fetchOutboundsTraffic,
    staleTime: Infinity,
  });

  const [xraySetting, setXraySettingState] = useState('');
  const [templateSettings, setTemplateSettingsState] = useState<XraySettingsValue | null>(null);
  const [outboundTestUrl, setOutboundTestUrlState] = useState(DEFAULT_TEST_URL);
  const [savedXraySetting, setSavedXraySetting] = useState('');
  const [savedOutboundTestUrl, setSavedOutboundTestUrl] = useState(DEFAULT_TEST_URL);
  const [inboundTags, setInboundTags] = useState<string[]>([]);
  const [routingSubjects, setRoutingSubjects] = useState<RoutingSubject[]>([]);
  const [clientReverseTags, setClientReverseTags] = useState<string[]>([]);
  const [subscriptionOutbounds, setSubscriptionOutbounds] = useState<unknown[]>([]);
  const [subscriptionOutboundTags, setSubscriptionOutboundTags] = useState<string[]>([]);
  const [outboundTestStates, setOutboundTestStates] = useState<Record<number, OutboundTestState>>({});
  // Subscription outbounds aren't in templateSettings.outbounds, so their test
  // results are keyed by tag rather than by index.
  const [subscriptionTestStates, setSubscriptionTestStates] = useState<Record<string, OutboundTestState>>({});
  const [testingAll, setTestingAll] = useState(false);

  const syncingRef = useRef(false);
  const xraySettingRef = useRef('');
  const outboundTestUrlRef = useRef(outboundTestUrl);
  const savedXraySettingRef = useRef(savedXraySetting);
  const savedOutboundTestUrlRef = useRef(savedOutboundTestUrl);
  const templateSettingsRef = useRef<XraySettingsValue | null>(null);
  const subscriptionOutboundsRef = useRef<unknown[]>([]);
  const probeFingerprintRef = useRef('');
  const invalidDraftRef = useRef('');
  const probeEpochRef = useRef(0);
  const probeSequenceRef = useRef(0);
  const probeRequestsRef = useRef(new Map<string, number>());
  const testAllRunRef = useRef<number | null>(null);

  const syncProbeContext = useCallback(() => {
    const fingerprint = JSON.stringify([templateSettingsRef.current, invalidDraftRef.current, subscriptionOutboundsRef.current,
      normalizeOutboundTestUrl(outboundTestUrlRef.current)]);
    if (fingerprint === probeFingerprintRef.current) return;
    probeFingerprintRef.current = fingerprint;
    probeEpochRef.current += 1;
    for (const key of probeRequestsRef.current.keys()) {
      if (!key.startsWith('exit:')) probeRequestsRef.current.delete(key);
    }
    setOutboundTestStates((prev) => Object.fromEntries(Object.entries(prev).filter(([key]) => Number(key) < 0 || isExitKey(Number(key)))));
    setSubscriptionTestStates({});
    testAllRunRef.current = null;
    setTestingAll(false);
  }, []);

  const beginProbe = useCallback((key: string) => {
    const request = ++probeSequenceRef.current;
    probeRequestsRef.current.set(key, request);
    return request;
  }, []);
  const isCurrentProbe = useCallback((key: string, request: number) => probeRequestsRef.current.get(key) === request, []);

  xraySettingRef.current = xraySetting;
  outboundTestUrlRef.current = outboundTestUrl;
  savedXraySettingRef.current = savedXraySetting;
  savedOutboundTestUrlRef.current = savedOutboundTestUrl;
  templateSettingsRef.current = templateSettings;
  subscriptionOutboundsRef.current = subscriptionOutbounds;

  useEffect(() => {
    if (!configQuery.data) return;
    const obj = configQuery.data;
    const pretty = JSON.stringify(obj.xraySetting, null, 2);
    const nextUrl = normalizeOutboundTestUrl(obj.outboundTestUrl || '');
    setInboundTags(obj.inboundTags || []);
    setRoutingSubjects(obj.routingSubjects || []);
    setClientReverseTags(obj.clientReverseTags || []);
    setSubscriptionOutbounds(obj.subscriptionOutbounds || []);
    subscriptionOutboundsRef.current = obj.subscriptionOutbounds || [];
    setSubscriptionOutboundTags(obj.subscriptionOutboundTags || []);
    const isDirty = savedXraySettingRef.current !== xraySettingRef.current
      || savedOutboundTestUrlRef.current !== normalizeOutboundTestUrl(outboundTestUrlRef.current);
    if (isDirty) { syncProbeContext(); return; }
    syncingRef.current = true;
    setXraySettingState(pretty);
    setTemplateSettingsState(obj.xraySetting);
    templateSettingsRef.current = obj.xraySetting;
    invalidDraftRef.current = '';
    xraySettingRef.current = pretty;
    setSavedXraySetting(pretty);
    syncingRef.current = false;
    setOutboundTestUrlState(nextUrl);
    outboundTestUrlRef.current = nextUrl;
    setSavedOutboundTestUrl(nextUrl);
    syncProbeContext();
  }, [configQuery.data, syncProbeContext]);

  const fetched = configQuery.data !== undefined || configQuery.isError;
  const fetchError = configQuery.error ? (configQuery.error as Error).message : '';

  const setXraySetting = useCallback((next: string) => {
    setXraySettingState(next);
    xraySettingRef.current = next;
    if (syncingRef.current) return;
    try {
      const parsed = JSON.parse(next);
      syncingRef.current = true;
      setTemplateSettingsState(parsed);
      templateSettingsRef.current = parsed;
      invalidDraftRef.current = '';
      syncingRef.current = false;
    } catch {
      invalidDraftRef.current = next;
    }
    syncProbeContext();
  }, [syncProbeContext]);

  const setTemplateSettings: SetTemplate = useCallback((nextOrFn) => {
    const next = typeof nextOrFn === 'function' ? nextOrFn(templateSettingsRef.current) : nextOrFn;
    templateSettingsRef.current = next;
    invalidDraftRef.current = '';
    setTemplateSettingsState(next);
    if (next != null && !syncingRef.current) {
      const pretty = JSON.stringify(next, null, 2);
      xraySettingRef.current = pretty;
      setXraySettingState(pretty);
    }
    syncProbeContext();
  }, [syncProbeContext]);

  const setOutboundTestUrl = useCallback((v: string) => {
    setOutboundTestUrlState(v);
    outboundTestUrlRef.current = v;
    syncProbeContext();
  }, [syncProbeContext]);

  const fetchAll = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: keys.xray.config() });
  }, [queryClient]);

  const fetchOutboundsTrafficCb = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: keys.xray.outboundsTraffic() });
  }, [queryClient]);

  const saveMut = useMutation({
    mutationFn: async (snapshot?: string) => {
      const sentXraySetting = snapshot ?? xraySettingRef.current;
      const sentTestUrl = normalizeOutboundTestUrl(outboundTestUrlRef.current);
      const msg = await HttpUtil.post('/panel/api/xray/update', {
        xraySetting: sentXraySetting,
        outboundTestUrl: sentTestUrl,
      });
      return { msg, sentXraySetting, sentTestUrl };
    },
    onSuccess: ({ msg, sentXraySetting, sentTestUrl }) => {
      if (!msg?.success) return;
      setSavedXraySetting(sentXraySetting);
      setSavedOutboundTestUrl(sentTestUrl);
      queryClient.invalidateQueries({ queryKey: keys.xray.config() });
    },
  });

  const resetTrafficMut = useMutation({
    mutationFn: (tag: string) =>
      HttpUtil.post('/panel/api/xray/resetOutboundsTraffic', { tag }),
    onSuccess: (msg) => {
      if (msg?.success) queryClient.invalidateQueries({ queryKey: keys.xray.outboundsTraffic() });
    },
  });

  const resetDefaultMut = useMutation({
    mutationFn: async (): Promise<Msg<XraySettingsValue>> => {
      const raw = await HttpUtil.get('/panel/api/setting/getDefaultJsonConfig');
      return parseMsg(raw, XraySettingsValueSchema, 'setting/getDefaultJsonConfig');
    },
    onSuccess: (msg) => {
      if (msg?.success && msg.obj) {
        const cloned = JSON.parse(JSON.stringify(msg.obj));
        setTemplateSettings(cloned);
      }
    },
  });

  const saveAll = useCallback(async (snapshot?: string) => {
    const { msg } = await saveMut.mutateAsync(snapshot);
    if (!msg?.success) throw new Error(msg?.msg || 'somethingWentWrong');
  }, [saveMut]);
  const resetOutboundsTraffic = useCallback(async (tag: string) => { await resetTrafficMut.mutateAsync(tag); }, [resetTrafficMut]);
  const resetToDefault = useCallback(async () => { await resetDefaultMut.mutateAsync(); }, [resetDefaultMut]);

  const spinning = saveMut.isPending || resetDefaultMut.isPending;

  /* One shared temporary Xray instance probes each batch in request order.
     Keep a result for every input, including missing results and failures. */
  const postOutboundTestBatch = useCallback(
    async (outbounds: unknown[], effMode: string): Promise<OutboundTestResult[]> => {
      const failAll = (error: string): OutboundTestResult[] =>
        outbounds.map(() => ({ success: false, error, mode: effMode }));
      try {
        const raw = await HttpUtil.post('/panel/api/xray/testOutbounds', {
          outbounds: JSON.stringify(outbounds),
          allOutbounds: JSON.stringify(templateSettingsRef.current?.outbounds || []),
          mode: effMode,
        });
        const msg = parseMsg(raw, OutboundTestResultListSchema, 'xray/testOutbounds');
        if (!msg?.success || !Array.isArray(msg.obj)) return failAll(msg?.msg || 'Unknown error');
        const list = msg.obj;
        return outbounds.map((_ob, i) => list[i] ?? { success: false, error: 'Missing result', mode: effMode });
      } catch (e) {
        return failAll(String(e));
      }
    },
    [],
  );

  const testOutbound = useCallback(
    async (index: number, outbound: unknown, mode = 'tcp', request = beginProbe(`tpl:${index}`)): Promise<OutboundTestResult | null> => {
      const key = `tpl:${index}`;
      if (!outbound || !isCurrentProbe(key, request)) return null;
      const effMode = mode === 'tcp' && isUdpOutbound(outbound) ? 'http' : mode;
      setOutboundTestStates((prev) => ({
        ...prev,
        [index]: { testing: true, result: null, mode: effMode },
      }));
      const [result] = await postOutboundTestBatch([outbound], effMode);
      if (!isCurrentProbe(key, request)) return null;
      setOutboundTestStates((prev) => isCurrentProbe(key, request) ? { ...prev, [index]: { testing: false, result } } : prev);
      return result.success ? result : null;
    },
    [postOutboundTestBatch, beginProbe, isCurrentProbe],
  );

  /* A host-level exit is not in templateSettings.outbounds and has no SOCKS
     inbound to route through, so it is timed by its own endpoint — but it lands
     in the same state map, because the table it renders in is the same table. */
  const testExit = useCallback(
    async (key: number, mode = 'tcp', request = beginProbe(`exit:${key}`)): Promise<OutboundTestResult | null> => {
      const requestKey = `exit:${key}`;
      if (!isCurrentProbe(requestKey, request)) return null;
      const id = exitIdFromKey(key);
      const effMode = mode === 'tcp' ? 'http' : mode;
      setOutboundTestStates((prev) => ({
        ...prev,
        [key]: { testing: true, result: null, mode: effMode },
      }));
      let result: OutboundTestResult;
      try {
        // JSON, like every other /egresses endpoint: form-encoding turns ids
        // into repeated keys and the body never reaches the decoder intact.
        const raw = await HttpUtil.post('/panel/api/egresses/test', { ids: [id], mode }, {
          headers: { 'Content-Type': 'application/json' },
        });
        const msg = parseMsg(raw, OutboundTestResultListSchema, 'egresses/test');
        result = (msg?.success && Array.isArray(msg.obj) ? msg.obj[0] : null)
          ?? { success: false, error: msg?.msg || 'no result', mode: effMode };
      } catch (e) {
        result = { success: false, error: String(e), mode: effMode };
      }
      if (!isCurrentProbe(requestKey, request)) return null;
      setOutboundTestStates((prev) => isCurrentProbe(requestKey, request) ? { ...prev, [key]: { testing: false, result } } : prev);
      return result.success ? result : null;
    },
    [beginProbe, isCurrentProbe],
  );

  // Test a subscription outbound (not present in templateSettings.outbounds);
  // results are keyed by tag in subscriptionTestStates.
  const testSubscriptionOutbound = useCallback(
    async (tag: string, outbound: unknown, mode = 'tcp', request = beginProbe(`sub:${tag}`)): Promise<OutboundTestResult | null> => {
      const key = `sub:${tag}`;
      if (!outbound || !tag || !isCurrentProbe(key, request)) return null;
      const effMode = mode === 'tcp' && isUdpOutbound(outbound) ? 'http' : mode;
      setSubscriptionTestStates((prev) => ({
        ...prev,
        [tag]: { testing: true, result: null, mode: effMode },
      }));
      const [result] = await postOutboundTestBatch([outbound], effMode);
      if (!isCurrentProbe(key, request)) return null;
      setSubscriptionTestStates((prev) => isCurrentProbe(key, request) ? { ...prev, [tag]: { testing: false, result } } : prev);
      return result.success ? result : null;
    },
    [postOutboundTestBatch, beginProbe, isCurrentProbe],
  );

  const testAllOutbounds = useCallback(async (mode = 'tcp', exitKeys: number[] = []) => {
    // Template outbounds key their results by index (outboundTestStates);
    // subscription outbounds aren't in the template, so they key by tag
    // (subscriptionTestStates). Both go through the same probe endpoint.
    // Exits are neither: their keys are passed in by the table that lists them,
    // because they live in the egress rows rather than in the xray config.
    const templateList = templateSettingsRef.current?.outbounds || [];
    const subList = (subscriptionOutboundsRef.current || []) as Array<{ tag?: string; protocol?: string }>;
    if ((templateList.length === 0 && subList.length === 0 && exitKeys.length === 0) || testAllRunRef.current !== null) return;
    const run = ++probeSequenceRef.current;
    const epoch = probeEpochRef.current;
    const isCurrentRun = () => epoch === probeEpochRef.current && testAllRunRef.current === run;
    testAllRunRef.current = run;
    setTestingAll(true);
    try {
      type TcpEntry =
        | { kind: 'tpl'; index: number; outbound: unknown; request: number }
        | { kind: 'sub'; tag: string; outbound: unknown; request: number };
      const tcpQueue: TcpEntry[] = [];
      // HTTP batches stay homogeneous (all template or all subscription) so a
      // tag shared between a template and a subscription outbound can't collide
      // inside one batch, and each batch's results route to one state map.
      const probeMode = mode === 'real' ? 'real' : 'http';
      const httpTplQueue: { index: number; outbound: unknown; request: number }[] = [];
      const httpSubQueue: { tag: string; outbound: unknown; request: number }[] = [];
      const enqueue = (ob: { tag?: string; protocol?: string }, kind: 'tpl' | 'sub', index: number, tag: string) => {
        const proto = ob?.protocol;
        if (proto === 'blackhole' || proto === 'loopback' || ob?.tag === 'blocked') return;
        // freedom ("direct") and dns aren't proxies — skip them in every mode.
        if (proto === 'freedom' || proto === 'dns') return;
        if (kind === 'sub' && !tag) return;
        const request = beginProbe(kind === 'tpl' ? `tpl:${index}` : `sub:${tag}`);
        const toHttp = mode !== 'tcp' || isUdpOutbound(ob);
        if (kind === 'tpl') {
          if (toHttp) httpTplQueue.push({ index, outbound: ob, request });
          else tcpQueue.push({ kind: 'tpl', index, outbound: ob, request });
        } else if (toHttp) {
          httpSubQueue.push({ tag, outbound: ob, request });
        } else {
          tcpQueue.push({ kind: 'sub', tag, outbound: ob, request });
        }
      };
      templateList.forEach((ob, i) => enqueue(ob, 'tpl', i, ''));
      subList.forEach((ob) => enqueue(ob, 'sub', -1, typeof ob?.tag === 'string' ? ob.tag : ''));

      // TCP probes are dial-only and cheap server-side; per-item requests
      // keep results landing one by one, each routed to its own state map.
      const runTcpLane = async () => {
        const queue = [...tcpQueue];
        const worker = async () => {
          while (queue.length > 0 && isCurrentRun()) {
            const item = queue.shift();
            if (!item) break;
            if (item.kind === 'sub') await testSubscriptionOutbound(item.tag, item.outbound, mode, item.request);
            else await testOutbound(item.index, item.outbound, mode, item.request);
          }
        };
        await Promise.all(Array.from({ length: Math.min(8, queue.length) }, () => worker()));
      };
      // HTTP probes go out as chunked batches — one temp xray spawn per
      // chunk instead of one per outbound, with results landing per chunk.
      const runTplHttpLane = async () => {
        for (let at = 0; at < httpTplQueue.length; at += HTTP_BATCH_CHUNK) {
          if (!isCurrentRun()) return;
          const chunk = httpTplQueue.slice(at, at + HTTP_BATCH_CHUNK).filter((item) => isCurrentProbe(`tpl:${item.index}`, item.request));
          if (!chunk.length) continue;
          setOutboundTestStates((prev) => {
            const next = { ...prev };
            for (const item of chunk) if (isCurrentProbe(`tpl:${item.index}`, item.request)) next[item.index] = { testing: true, result: null, mode: probeMode };
            return next;
          });
          const results = await postOutboundTestBatch(chunk.map((c) => c.outbound), probeMode);
          if (!isCurrentRun()) return;
          setOutboundTestStates((prev) => {
            const next = { ...prev };
            chunk.forEach((item, i) => {
              if (isCurrentProbe(`tpl:${item.index}`, item.request)) next[item.index] = { testing: false, result: results[i] };
            });
            return next;
          });
        }
      };
      const runSubHttpLane = async () => {
        for (let at = 0; at < httpSubQueue.length; at += HTTP_BATCH_CHUNK) {
          if (!isCurrentRun()) return;
          const chunk = httpSubQueue.slice(at, at + HTTP_BATCH_CHUNK).filter((item) => isCurrentProbe(`sub:${item.tag}`, item.request));
          if (!chunk.length) continue;
          setSubscriptionTestStates((prev) => {
            const next = { ...prev };
            for (const item of chunk) if (isCurrentProbe(`sub:${item.tag}`, item.request)) next[item.tag] = { testing: true, result: null, mode: probeMode };
            return next;
          });
          const results = await postOutboundTestBatch(chunk.map((c) => c.outbound), probeMode);
          if (!isCurrentRun()) return;
          setSubscriptionTestStates((prev) => {
            const next = { ...prev };
            chunk.forEach((item, i) => {
              if (isCurrentProbe(`sub:${item.tag}`, item.request)) next[item.tag] = { testing: false, result: results[i] };
            });
            return next;
          });
        }
      };
      // HTTP batches must not overlap: the backend serialises them with a
      // non-blocking lock and rejects a second concurrent batch ("Another
      // outbound test is already running"). Run the template and subscription
      // HTTP lanes one after the other; TCP probes don't take that lock, so
      // they still run alongside.
      const runHttpLane = async () => {
        await runTplHttpLane();
        await runSubHttpLane();
      };
      /* Exits take no xray instance and so no batch lock, but each one holds a
         connection open through its own tunnel for seconds — a few at a time. */
      const runExitLane = async () => {
        const queue = exitKeys.map((key) => ({ key, request: beginProbe(`exit:${key}`) }));
        const worker = async () => {
          while (queue.length > 0) {
            const item = queue.shift();
            if (!item) break;
            await testExit(item.key, mode, item.request);
          }
        };
        await Promise.all(Array.from({ length: Math.min(4, queue.length) }, () => worker()));
      };
      await Promise.all([runTcpLane(), runHttpLane(), runExitLane()]);
    } finally {
      if (testAllRunRef.current === run) {
        testAllRunRef.current = null;
        setTestingAll(false);
      }
    }
  }, [testOutbound, testExit, testSubscriptionOutbound, postOutboundTestBatch, beginProbe, isCurrentProbe]);

  const saveDisabled = savedXraySetting === xraySetting
    && savedOutboundTestUrl === normalizeOutboundTestUrl(outboundTestUrl);

  const outboundsTraffic = useMemo(() => trafficQuery.data ?? [], [trafficQuery.data]);

  return useMemo(
    () => ({
      fetched,
      spinning,
      saveDisabled,
      fetchError,
      xraySetting,
      setXraySetting,
      templateSettings,
      setTemplateSettings,
      outboundTestUrl,
      setOutboundTestUrl,
      inboundTags,
      routingSubjects,
      clientReverseTags,
      subscriptionOutbounds,
      subscriptionOutboundTags,
      outboundsTraffic,
      outboundTestStates,
      subscriptionTestStates,
      testingAll,
      fetchAll,
      fetchOutboundsTraffic: fetchOutboundsTrafficCb,
      resetOutboundsTraffic,
      testOutbound,
      testExit,
      testSubscriptionOutbound,
      testAllOutbounds,
      saveAll,
      resetToDefault,
    }),
    [
      fetched,
      spinning,
      saveDisabled,
      fetchError,
      xraySetting,
      setXraySetting,
      templateSettings,
      setTemplateSettings,
      outboundTestUrl,
      setOutboundTestUrl,
      inboundTags,
      routingSubjects,
      clientReverseTags,
      subscriptionOutbounds,
      subscriptionOutboundTags,
      outboundsTraffic,
      outboundTestStates,
      subscriptionTestStates,
      testingAll,
      fetchAll,
      fetchOutboundsTrafficCb,
      resetOutboundsTraffic,
      testOutbound,
      testExit,
      testSubscriptionOutbound,
      testAllOutbounds,
      saveAll,
      resetToDefault,
    ],
  );
}
