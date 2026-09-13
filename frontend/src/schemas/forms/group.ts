import { z } from 'zod';

export const GroupFormSchema = z.object({
  name: z.string().trim().min(1, 'groupParity.nameRequired'),
  quotaGiB: z.number().min(0, 'pages.clients.errNotNegative').max(Number.MAX_SAFE_INTEGER / 1073741824, 'telemetry.quotaTooLarge'),
  inboundIds: z.array(z.number()),
});

export type GroupFormValues = z.infer<typeof GroupFormSchema>;
