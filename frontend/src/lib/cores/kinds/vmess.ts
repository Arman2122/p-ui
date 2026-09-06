import { createDefaultVmessInboundSettings } from '@/lib/xray/inbound-defaults';
import { VmessClientSchema } from '@/schemas/protocols/inbound';

import type { KindModule } from './module';

export const vmess: KindModule = {
  defaultSettings: createDefaultVmessInboundSettings,
  clientSchema: VmessClientSchema,
  multiUser: true,
  shareLink: true,
  nodeEligible: true,
  settingsTab: false,
};
