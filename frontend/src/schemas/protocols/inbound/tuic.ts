import { z } from 'zod';

// A TUIC client: the uuid and password sing-box authenticates, both minted by
// the backend when left empty; the email is the name its traffic is counted under.
export const TuicClientSchema = z.object({
  id: z.string().default(''),
  password: z.string().default(''),
  email: z.string().min(1),
  limitIp: z.number().int().min(0).default(0),
  totalGB: z.number().int().min(0).default(0),
  expiryTime: z.number().int().default(0),
  enable: z.boolean().default(true),
  tgId: z.union([z.number(), z.string()]).transform((v) => Number(v) || 0).default(0),
  subId: z.string().default(''),
  comment: z.string().default(''),
  reset: z.number().int().min(0).default(0),
  created_at: z.number().int().optional(),
  updated_at: z.number().int().optional(),
});
export type TuicClient = z.infer<typeof TuicClientSchema>;

export const TUIC_CONGESTION_CONTROLS = ['bbr', 'cubic', 'new_reno'] as const;

// TUIC over QUIC, served by the bundled sing-box: TLS is mandatory, so the
// certificate pair is part of the inbound rather than of a stream setting.
export const TuicInboundSettingsSchema = z.object({
  certFile: z.string().default(''),
  keyFile: z.string().default(''),
  serverName: z.string().default(''),
  congestionControl: z.enum(TUIC_CONGESTION_CONTROLS).default('bbr'),
  zeroRttHandshake: z.boolean().default(false),
  /* Sends the daemon's own outbound traffic through the panel's loopback
     bridge into Xray's router, which is what lets a routing rule name this
     inbound. `routeXrayPort` is the bridge's port: allocated by the backend,
     never typed, and declared here because the object strips what it does not
     declare and the save would drop it. */
  routeThroughXray: z.boolean().optional(),
  routeXrayPort: z.number().int().min(0).max(65535).optional(),
  clients: z.array(TuicClientSchema).default([]),
});
export type TuicInboundSettings = z.infer<typeof TuicInboundSettingsSchema>;
