import { z } from 'zod';

import { WireguardClientSchema, optionalClearedInt } from './wireguard';

/*
Kernel WireGuard (`wgkernel`) is its own core: the panel converges a real
`pwg<id>` netlink device instead of handing a config to Xray. Its clients carry
the same credentials as an xray `wireguard` client, so that schema is reused
rather than forked — only the device half differs.

`address` is the tunnel address the device itself answers on and has no
equivalent in the xray schema (xray's userspace tunnel takes its address from
the outbound). It is required: a device with none routes nothing back.
`noKernelTun` and `domainStrategy` are absent on purpose — both describe how
xray fakes a TUN device, which the kernel does not need to be told.
*/
export const WgkernelInboundSettingsSchema = z.object({
  address: z.array(z.string().min(1)).min(1),
  secretKey: z.string().min(1),
  mtu: optionalClearedInt(z.number().int().min(1)),
  dns: z.string().optional(),
  /* Routing mark the kernel stamps on the device's own outgoing packets. No
     form field: policy routing is an operator concern, edited via Advanced. */
  fwmark: optionalClearedInt(z.number().int().min(0)),
  clients: z.array(WireguardClientSchema).default([]),
});
export type WgkernelInboundSettings = z.infer<typeof WgkernelInboundSettingsSchema>;
