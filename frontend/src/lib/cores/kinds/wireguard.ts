import { createDefaultWireguardInboundSettings } from '@/lib/xray/inbound-defaults';
import { WireguardClientSchema } from '@/schemas/protocols/inbound';

import { UDP, type KindModule } from './module';

export const wireguard: KindModule = {
  defaultSettings: createDefaultWireguardInboundSettings,
  clientSchema: WireguardClientSchema,
  transports: () => UDP,
  multiUser: true,
  shareLink: false,
  nodeEligible: true,
  /* Always a UDP listener: the stream tab is sockopt alone. */
  stream: 'none',
};
