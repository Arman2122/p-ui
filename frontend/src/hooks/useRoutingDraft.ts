import { useCallback, useMemo, useRef, useState } from 'react';

import { useRoutingRulesQuery } from '@/api/queries/useRoutingQuery';
import { useRoutingMutations } from '@/api/queries/useRoutingMutations';
import { recordToPayload, type RoutingRuleRecord, type RoutingRulePayload } from '@/schemas/api/routing';

/*
Routing rules edit as a DRAFT, like every other part of the Xray page.

They used to write straight through: the dialog's OK posted the rule, and
"move up" posted a new order — each of which raises the restart flag, so the
change reached live traffic within the next reconcile whether or not the
operator was finished. Routing is first-match, so nudging a row up IS a routing
change. Meanwhile the page's Save button stayed disabled, because it watches the
template and these rules are not in it: there was no review step and no way to
ask for one.

A new rule has no id until it is written, so drafts carry a negative placeholder
and flush() resolves it. Order is the array's own order; the server is told once,
at the end, and only when it actually differs.
*/

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
}

export function isDraftRule(record: RoutingRuleRecord): boolean {
  return record.id < 0;
}

export function useRoutingDraft(): RoutingDraft {
  const { data: serverRules = [] } = useRoutingRulesQuery();
  const mutations = useRoutingMutations();
  const [draft, setDraft] = useState<RoutingRuleRecord[] | null>(null);

  const rules = draft ?? serverRules;
  const rulesRef = useRef<RoutingRuleRecord[]>([]);
  rulesRef.current = rules;
  const serverRef = useRef<RoutingRuleRecord[]>([]);
  serverRef.current = serverRules;

  const edit = useCallback((next: (current: RoutingRuleRecord[]) => RoutingRuleRecord[]) => {
    setDraft((current) => next(current ?? serverRef.current));
  }, []);

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

  const discard = useCallback(() => setDraft(null), []);

  const dirty = useMemo(() => {
    if (draft == null) return false;
    if (draft.length !== serverRules.length) return true;
    return draft.some((rule, index) => {
      const server = serverRules[index];
      if (!server || server.id !== rule.id) return true;
      return JSON.stringify(recordToPayload(rule)) !== JSON.stringify(recordToPayload(server));
    });
  }, [draft, serverRules]);

  /*
    Deletes first, so a rule freed from a port or tag cannot collide with one
    being added in the same save. Then updates, then adds, then the order --
    which can only be sent once every id exists.
  */
  const flush = useCallback(async () => {
    const desired = rulesRef.current;
    const server = serverRef.current;
    if (draft == null) return;

    const keep = new Set(desired.map((r) => r.id));
    for (const record of server) {
      if (!keep.has(record.id)) await mutations.remove(record.id);
    }

    const byId = new Map(server.map((r) => [r.id, r]));
    const finalIds: number[] = [];
    for (const rule of desired) {
      const payload = recordToPayload(rule);
      if (isDraftRule(rule)) {
        const created = await mutations.add(payload);
        const id = (created?.obj as { id?: number } | undefined)?.id;
        if (typeof id === 'number') finalIds.push(id);
        continue;
      }
      const existing = byId.get(rule.id);
      if (existing && JSON.stringify(recordToPayload(existing)) !== JSON.stringify(payload)) {
        await mutations.update(rule.id, payload);
      }
      finalIds.push(rule.id);
    }

    /* Only when it actually moved: the reorder endpoint raises the restart flag
       like every other write, and a save that changed one remark should not
       reload the core for an order nobody touched. */
    const serverOrder = server.map((r) => r.id).join(',');
    if (finalIds.length > 0 && finalIds.join(',') !== serverOrder) {
      await mutations.reorder(finalIds);
    }
    setDraft(null);
  }, [draft, mutations]);

  return { rules, dirty, addRule, updateRule, removeRule, reorderRules, discard, flush };
}
