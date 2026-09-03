import { z } from 'zod';

// An OpenConnect client: a login name and password the panel mints when empty.
// Clients present no certificate, so there is nothing else to carry.
export const OcservClientSchema = z.object({
  username: z.string().default(''),
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
export type OcservClient = z.infer<typeof OcservClientSchema>;

/* The distribution's ocserv, one supervised process per inbound, serving the
   AnyConnect protocol over TLS and DTLS on one port. The certificate pair is
   minted by the backend on the first save and kept on every later one; an
   operator may paste their own, which is what the native client needs. */
export const OcservInboundSettingsSchema = z.object({
  subnet: z.string().default('10.11.0.0/24'),
  dns: z.array(z.string()).default([]),
  redirectGateway: z.boolean().default(true),
  serverCert: z.string().default(''),
  serverKey: z.string().default(''),
  camouflage: z.boolean().default(false),
  camouflageSecret: z.string().default(''),
  clients: z.array(OcservClientSchema).default([]),
});
export type OcservInboundSettings = z.infer<typeof OcservInboundSettingsSchema>;
