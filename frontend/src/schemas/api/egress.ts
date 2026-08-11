import { z } from 'zod';

import { EgressSchema } from '@/generated/zod';
import type { Egress } from '@/generated/zod';

export type EgressRecord = Egress;

export const EgressListSchema = z.array(EgressSchema);

/* What stops this host carrying an egress. Refusals and notes are backend
   sentences naming a sysctl or a device, so they are rendered, never translated. */
export const EgressPreflightSchema = z.object({
  ok: z.boolean(),
  refusals: z.array(z.string()),
  notes: z.array(z.string()),
});
export type EgressPreflight = z.infer<typeof EgressPreflightSchema>;

/* One driver ships, so `type` is a constant rather than a picker; `settings` is
   the per-type column no driver reads yet and the form round-trips it untouched. */
export const EGRESS_TYPE = 'xray-tun';

export const EgressFormSchema = z.object({
  id: z.number().int().default(0),
  remark: z.string().trim().max(256).default(''),
  target: z.string().trim().min(1, 'pages.xray.egress.targetRequired').default(''),
  enable: z.boolean().default(true),
});
export type EgressFormValues = z.infer<typeof EgressFormSchema>;

/* Everything the operator owns. The timestamps are the database's, so a write
   that carried them back would be claiming to set them. */
export type EgressPayload = Omit<Egress, 'createdAt' | 'updatedAt'>;
