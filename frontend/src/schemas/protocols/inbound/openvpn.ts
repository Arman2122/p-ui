import { z } from 'zod';

// An OpenVPN client: a login name and password the panel mints when empty,
// and the wrapped tls-crypt-v2 key the panel mints on save (never typed).
export const OpenvpnClientSchema = z.object({
  username: z.string().default(''),
  password: z.string().default(''),
  tlsCryptV2: z.string().default(''),
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
export type OpenvpnClient = z.infer<typeof OpenvpnClientSchema>;

export const OPENVPN_PROTOS = ['udp', 'tcp'] as const;

/* The distribution's OpenVPN, one supervised process per inbound. The three
   identity fields are minted by the backend on the first save and kept on
   every later one; the form never shows them. */
export const OpenvpnInboundSettingsSchema = z.object({
  proto: z.enum(OPENVPN_PROTOS).default('udp'),
  subnet: z.string().default('10.9.0.0/24'),
  dns: z.array(z.string()).default([]),
  redirectGateway: z.boolean().default(true),
  serverCert: z.string().default(''),
  serverKey: z.string().default(''),
  tlsCryptV2ServerKey: z.string().default(''),
  clients: z.array(OpenvpnClientSchema).default([]),
});
export type OpenvpnInboundSettings = z.infer<typeof OpenvpnInboundSettingsSchema>;
