import type { z } from 'zod';

import type { RoutingSubjectSchema } from '@/schemas/xray';

export type RoutingSubject = z.infer<typeof RoutingSubjectSchema>;

/* An option the "From" picker renders. disabled carries reasonKey, which is a
   translation key rather than a sentence so both locales stay in charge. */
export interface InboundTagOption {
  value: string;
  disabled: boolean;
  reasonKey?: string;
}

export interface RuleRow {
  key: number;
  /* Which store this row lives in. The only place that distinction surfaces:
     an operator sees one list in true evaluation order. */
  source?: 'intent' | 'template';
  storeIndex?: number;
  enabled?: boolean;
  domain?: string;
  ip?: string;
  port?: string;
  sourcePort?: string;
  vlessRoute?: string;
  network?: string;
  sourceIP?: string;
  user?: string;
  inboundTag?: string;
  protocol?: string;
  attrs?: string;
  outboundTag?: string;
  balancerTag?: string;
}
