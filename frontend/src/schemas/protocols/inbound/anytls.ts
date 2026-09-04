import { z } from 'zod';

// An AnyTLS client: one password, minted by the backend when left empty; the
// email is the name its traffic is counted under.
export const AnytlsClientSchema = z.object({
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
export type AnytlsClient = z.infer<typeof AnytlsClientSchema>;

// AnyTLS over TCP, served by the bundled sing-box: TLS is the protocol, so the
// certificate pair is part of the inbound. paddingScheme is one line per entry.
export const AnytlsInboundSettingsSchema = z.object({
  certFile: z.string().default(''),
  keyFile: z.string().default(''),
  serverName: z.string().default(''),
  paddingScheme: z.array(z.string()).default([]),
  /* Sends the daemon's own outbound traffic through the panel's loopback
     bridge into Xray's router, which is what lets a routing rule name this
     inbound. `routeXrayPort` is the bridge's port: allocated by the backend,
     never typed, and declared here because the object strips what it does not
     declare and the save would drop it. */
  routeThroughXray: z.boolean().optional(),
  routeXrayPort: z.number().int().min(0).max(65535).optional(),
  clients: z.array(AnytlsClientSchema).default([]),
});
export type AnytlsInboundSettings = z.infer<typeof AnytlsInboundSettingsSchema>;
