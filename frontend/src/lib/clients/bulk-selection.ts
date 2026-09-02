/* A bulk operation acts on an explicit email list, or on everything the
   list page's filter matches, resolved by the server. */
export type BulkSelection = { emails: string[] } | { filter: Record<string, unknown> };

export function isEmptySelection(selection: BulkSelection): boolean {
  return 'emails' in selection && selection.emails.length === 0;
}

/* The page's paged query carries paging and sort keys the server ignores for
   a selection; they are dropped so the body says what it means. */
export function filterSelection(query: Record<string, unknown> | null): BulkSelection {
  const filter: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(query ?? {})) {
    if (key === 'page' || key === 'pageSize' || key === 'sort' || key === 'order') continue;
    if (value === undefined || value === null || value === '') continue;
    filter[key] = value;
  }
  return { filter };
}

export function selectionBody(selection: BulkSelection): { emails?: string[]; filter?: Record<string, unknown> } {
  return 'emails' in selection ? { emails: selection.emails } : { filter: selection.filter };
}
