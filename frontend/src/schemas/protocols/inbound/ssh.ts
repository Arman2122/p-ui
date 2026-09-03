import { z } from 'zod';

// An SSH client: a login name the client types (an email is not one; its @
// breaks ssh:// URLs and Injector payloads) and a password. Both are minted by
// the backend when left empty.
export const SshClientSchema = z.object({
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
export type SshClient = z.infer<typeof SshClientSchema>;

export const SSH_TRANSPORTS = ['tcp', 'ws', 'wss'] as const;
export type SshTransport = (typeof SSH_TRANSPORTS)[number];

// The in-process SSH server: no Xray, no stream settings. One inbound is one
// transport on one port; ws answers both the bare 101 the HTTP-Injector family
// sends and a framed RFC 6455 upgrade, and wss is the same over TLS with the
// pair named here. Forwards are TCP only.
export const SshInboundSettingsSchema = z.object({
  transport: z.enum(SSH_TRANSPORTS).default('tcp'),
  path: z.string().default(''),
  certFile: z.string().default(''),
  keyFile: z.string().default(''),
  banner: z.string().default(''),
  clients: z.array(SshClientSchema).default([]),
});
export type SshInboundSettings = z.infer<typeof SshInboundSettingsSchema>;
