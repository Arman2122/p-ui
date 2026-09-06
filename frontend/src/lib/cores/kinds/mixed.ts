import { createDefaultMixedInboundSettings } from '@/lib/xray/inbound-defaults';

import type { KindModule } from './module';

export const mixed: KindModule = {
  defaultSettings: createDefaultMixedInboundSettings,
  clientSchema: null,
  multiUser: false,
  shareLink: false,
};
