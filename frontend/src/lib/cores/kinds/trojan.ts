import { createDefaultTrojanInboundSettings } from '@/lib/xray/inbound-defaults';
import { TrojanClientSchema } from '@/schemas/protocols/inbound';

import type { KindModule } from './module';

export const trojan: KindModule = {
  defaultSettings: createDefaultTrojanInboundSettings,
  clientSchema: TrojanClientSchema,
  multiUser: true,
  shareLink: true,
  nodeEligible: true,
  settingsTab: false,
  fallbackHost: true,
};
