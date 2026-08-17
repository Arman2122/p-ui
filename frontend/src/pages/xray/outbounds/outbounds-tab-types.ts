import type { EgressRecord } from '@/schemas/api/egress';

export interface OutboundRow {
  key: number;
  tag?: string;
  protocol?: string;
  streamSettings?: { network?: string; security?: string };
  settings?: Record<string, unknown>;
  /* Set only on a host-level exit. Where traffic leaves is one question, so the
     two kinds share a table; what makes them is not, so they keep separate
     editors, and every handler reaching into the xray config branches here. */
  egress?: EgressRecord;
}
