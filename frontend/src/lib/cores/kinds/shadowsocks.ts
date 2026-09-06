import { createDefaultShadowsocksInboundSettings } from '@/lib/xray/inbound-defaults';
import { isSSMultiUser } from '@/lib/xray/protocol-capabilities';
import { ShadowsocksClientSchema } from '@/schemas/protocols/inbound';

import type { KindModule } from './module';

export const shadowsocks: KindModule = {
  defaultSettings: () => createDefaultShadowsocksInboundSettings(),
  clientSchema: ShadowsocksClientSchema,
  /* Whether the cipher carries a client list is the capability table's call. */
  multiUser: (settings) => isSSMultiUser({ protocol: 'shadowsocks', settings }),
  shareLink: true,
};
