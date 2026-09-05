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
// pair named here. UDP rides the tunnel through the UDPGW port when enabled.
export const SshInboundSettingsSchema = z.object({
  transport: z.enum(SSH_TRANSPORTS).default('tcp'),
  path: z.string().default(''),
  certFile: z.string().default(''),
  keyFile: z.string().default(''),
  banner: z.string().default(''),
  udpgw: z.boolean().default(false),
  udpgwPort: z.number().int().min(1).max(65535).default(7300),
  /* Sends every forward through the panel's loopback bridge into Xray's
     router, which is what lets a routing rule name this inbound.
     `routeXrayPort` is the bridge's port: allocated by the backend, never
     typed, and declared here because the object strips what it does not
     declare and the save would drop it. */
  routeThroughXray: z.boolean().optional(),
  routeXrayPort: z.number().int().min(0).max(65535).optional(),
  clients: z.array(SshClientSchema).default([]),
});
export type SshInboundSettings = z.infer<typeof SshInboundSettingsSchema>;
