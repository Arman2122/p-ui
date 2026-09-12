import { useCallback, useMemo, useRef, useState } from 'react';

import { useRoutingRulesQuery } from '@/api/queries/useRoutingQuery';
import { useRoutingMutations } from '@/api/queries/useRoutingMutations';
import { recordToPayload, type RoutingRuleRecord, type RoutingRulePayload } from '@/schemas/api/routing';

/* Routing writes affect live traffic, so edits stay local until flush resolves
   temporary IDs and persists the requested first-match order. */

/* Negative and decreasing, so a draft id can never collide with a real one and
   two new rules in one session stay distinct. */
let nextDraftId = -1;

export interface RoutingDraft {
  rules: RoutingRuleRecord[];
  dirty: boolean;
  addRule: (payload: RoutingRulePayload) => void;
  updateRule: (id: number, payload: RoutingRulePayload) => void;
  removeRule: (id: number) => void;
  reorderRules: (rules: RoutingRuleRecord[]) => void;
  discard: () => void;
  flush: () => Promise<void>;
  loading?: boolean;
  error?: Error | null;
  retry?: () => void;
}

export function isDraftRule(record: RoutingRuleRecord): boolean {
  return record.id < 0;
}

export function useRoutingDraft(): RoutingDraft {
  const { data: serverRules = [], isPending: loading, error, refetch } = useRoutingRulesQuery();
  const mutations = useRoutingMutations();
  const [draft, setDraft] = useState<RoutingRuleRecord[] | null>(null);
  const [saveIncomplete, setSaveIncomplete] = useState(false);
  const savedRef = useRef<RoutingRuleRecord[] | null>(null);
  const retryUpdates = useRef(new Set<number>());
  const retryOrder = useRef(false);
  const editRevision = useRef(0);

  const rules = draft ?? serverRules;
  const rulesRef = useRef<RoutingRuleRecord[]>([]);
  rulesRef.current = rules;
  const serverRef = useRef<RoutingRuleRecord[]>([]);
  serverRef.current = serverRules;

  const edit = useCallback((next: (current: RoutingRuleRecord[]) => RoutingRuleRecord[]) => {
    if (loading || error) return;
    editRevision.current += 1;
    setDraft((current) => next(current ?? serverRef.current));
  }, [loading, error]);

  const addRule = useCallback((payload: RoutingRulePayload) => {
    edit((current) => [...current, { ...payload, id: nextDraftId-- } as unknown as RoutingRuleRecord]);
  }, [edit]);

  const updateRule = useCallback((id: number, payload: RoutingRulePayload) => {
    edit((current) => current.map((r) => (r.id === id ? ({ ...r, ...payload } as RoutingRuleRecord) : r)));
  }, [edit]);

  const removeRule = useCallback((id: number) => {
    edit((current) => current.filter((r) => r.id !== id));
  }, [edit]);

  const reorderRules = useCallback((next: RoutingRuleRecord[]) => {
    edit(() => [...next]);
  }, [edit]);

  const discard = useCallback(() => {
    editRevision.current += 1;
    savedRef.current = null;
    retryUpdates.current.clear();
    retryOrder.current = false;
    setSaveIncomplete(false);
    setDraft(null);
  }, []);
  const retry = useCallback(() => { void refetch(); }, [refetch]);

  const dirty = useMemo(() => {
    if (draft == null) return false;
    if (saveIncomplete) return true;
    if (draft.length !== serverRules.length) return true;
    return draft.some((rule, index) => {
      const server = serverRules[index];
      if (!server || server.id !== rule.id) return true;
      return JSON.stringify(recordToPayload(rule)) !== JSON.stringify(recordToPayload(server));
    });
  }, [draft, serverRules, saveIncomplete]);

  /* Delete freed names first; order is sent only after every rule has an ID. */
  const flush = useCallback(async () => {
    const desired = rulesRef.current;
    if (draft == null) return;
    if (error) throw error;
    if (loading) throw new Error('Routing rules are still loading');
    const revision = editRevision.current;

    /* Query invalidation can lag a write. Retain acknowledgements across a
       failed flush so retries cannot recreate rules or repeat deleted IDs. */
    let saved = savedRef.current ?? [...serverRef.current];
    savedRef.current = saved;
    const orderBeforeSave = saved.map((rule) => rule.id).join(',');
    const acknowledge = (next: RoutingRuleRecord[]) => {
      saved = next;
      savedRef.current = next;
    };
    const requireSuccess = (msg: { success?: boolean; msg?: string } | null | undefined, fallback: string) => {
      if (!msg?.success) throw new Error(msg?.msg || fallback);
    };

    try {
      const keep = new Set(desired.map((r) => r.id));
      for (const record of saved) {
        if (keep.has(record.id)) continue;
        requireSuccess(await mutations.remove(record.id), `Failed to delete routing rule ${record.id}`);
        acknowledge(saved.filter((rule) => rule.id !== record.id));
        retryUpdates.current.delete(record.id);
      }

      const finalIds: number[] = [];
      for (const rule of desired) {
        const payload = recordToPayload(rule);
        if (isDraftRule(rule)) {
          const created = await mutations.add(payload);
          const id = (created?.obj as { id?: number } | undefined)?.id;
          if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0) {
            requireSuccess(created, 'Failed to create routing rule');
            throw new Error('The saved routing rule has no valid ID; reload the rules before saving again');
          }
          acknowledge([...saved, { ...rule, id }]);
          const assignId = (rows: RoutingRuleRecord[]) => rows.map((row) => row.id === rule.id ? { ...row, id } : row);
          rulesRef.current = assignId(rulesRef.current);
          setDraft((current) => current == null ? null : assignId(current));
          if (!created?.success) retryUpdates.current.add(id);
          requireSuccess(created, 'Failed to apply the saved routing rule');
          finalIds.push(id);
          continue;
        }
        const existing = saved.find((record) => record.id === rule.id);
        if (!existing) throw new Error(`Routing rule ${rule.id} is no longer available; reload the rules before saving again`);
        if (retryUpdates.current.has(rule.id) || JSON.stringify(recordToPayload(existing)) !== JSON.stringify(payload)) {
          retryUpdates.current.add(rule.id);
          requireSuccess(await mutations.update(rule.id, payload), `Failed to update routing rule ${rule.id}`);
          acknowledge(saved.map((record) => record.id === rule.id ? rule : record));
          retryUpdates.current.delete(rule.id);
        }
        finalIds.push(rule.id);
      }

      /* Avoid restarting the core for saves that did not change priority. */
      if (finalIds.length > 0 && (retryOrder.current || finalIds.join(',') !== orderBeforeSave)) {
        retryOrder.current = true;
        requireSuccess(await mutations.reorder(finalIds), 'Failed to save routing rule order');
        const byId = new Map(saved.map((rule) => [rule.id, rule]));
        acknowledge(finalIds.map((id) => byId.get(id)!));
      }

      retryOrder.current = false;
      setSaveIncomplete(false);
      if (revision === editRevision.current) {
        savedRef.current = null;
        setDraft(null);
      }
    } catch (cause) {
      setSaveIncomplete(true);
      throw cause instanceof Error ? cause : new Error('Failed to save routing rules');
    }
  }, [draft, mutations, error, loading]);

  return { rules, dirty, addRule, updateRule, removeRule, reorderRules, discard, flush, loading, error, retry };
}
