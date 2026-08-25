import { z } from 'zod';

export const MixedAuthSchema = z.enum(['password', 'noauth']);
export type MixedAuth = z.infer<typeof MixedAuthSchema>;

// SOCKS/HTTP combined inbound. When auth==='noauth' the `accounts` field is
// omitted from the wire payload (the panel writes `undefined`), so we accept
// either an array or absence here.
export const MixedAccountSchema = z.object({
  user: z.string().min(1),
  pass: z.string().min(1),
});
export type MixedAccount = z.infer<typeof MixedAccountSchema>;

export const MixedInboundSettingsSchema = z.object({
  auth: MixedAuthSchema.default('password'),
  accounts: z.array(MixedAccountSchema).optional(),
  udp: z.boolean().default(false),
  // No default. xray maps this to the SOCKS UDP hub's bind address and
  // advertises it back to the client; absent, it uses the real local address.
  ip: z.string().optional(),
});
export type MixedInboundSettings = z.infer<typeof MixedInboundSettingsSchema>;
