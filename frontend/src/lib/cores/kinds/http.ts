import { createDefaultHttpInboundSettings } from '@/lib/xray/inbound-defaults';

import type { KindModule } from './module';

export const http: KindModule = {
  defaultSettings: createDefaultHttpInboundSettings,
  clientSchema: null,
  multiUser: false,
  shareLink: false,
};
