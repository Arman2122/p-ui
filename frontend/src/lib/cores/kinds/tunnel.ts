import { createDefaultTunnelInboundSettings } from '@/lib/xray/inbound-defaults';

import type { KindModule } from './module';

export const tunnel: KindModule = {
  defaultSettings: createDefaultTunnelInboundSettings,
  clientSchema: null,
  multiUser: false,
  shareLink: false,
  /* A transparent forwarder: its stream tab is sockopt, and no mask applies. */
  stream: 'none',
  transportMasks: false,
};
