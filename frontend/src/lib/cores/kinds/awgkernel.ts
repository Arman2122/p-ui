import { createDefaultWgkernelInboundSettings } from '@/lib/xray/inbound-defaults';
import { WireguardClientSchema } from '@/schemas/protocols/inbound';

import { UDP, type KindModule } from './module';

/* The device is kernel WireGuard's; only the obfuscation is this kind's own. */
export const awgkernel: KindModule = {
  defaultSettings: createDefaultWgkernelInboundSettings,
  clientSchema: WireguardClientSchema,
  transports: () => UDP,
  multiUser: true,
  shareLink: false,
};
