import { createDefaultTunnelInboundSettings } from '@/lib/xray/inbound-defaults';

import type { KindModule } from './module';

export const tunnel: KindModule = {
  defaultSettings: createDefaultTunnelInboundSettings,
  clientSchema: null,
  multiUser: false,
  shareLink: false,
};
