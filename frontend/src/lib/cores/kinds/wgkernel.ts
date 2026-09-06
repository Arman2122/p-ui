import { createDefaultWgkernelInboundSettings } from '@/lib/xray/inbound-defaults';
import { WireguardClientSchema } from '@/schemas/protocols/inbound';

import { UDP, type KindModule } from './module';

export const wgkernel: KindModule = {
  defaultSettings: createDefaultWgkernelInboundSettings,
  clientSchema: WireguardClientSchema,
  transports: () => UDP,
  multiUser: true,
  shareLink: false,
};
