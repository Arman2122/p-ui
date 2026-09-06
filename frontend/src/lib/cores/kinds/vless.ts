import { createDefaultVlessInboundSettings } from '@/lib/xray/inbound-defaults';
import { VlessClientSchema } from '@/schemas/protocols/inbound';

import type { KindModule } from './module';

export const vless: KindModule = {
  defaultSettings: createDefaultVlessInboundSettings,
  clientSchema: VlessClientSchema,
  multiUser: true,
  shareLink: true,
};
