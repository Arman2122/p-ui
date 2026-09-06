import { createDefaultSshInboundSettings } from '@/lib/xray/inbound-defaults';
import { SshClientSchema } from '@/schemas/protocols/inbound';

import { TCP, type KindModule } from './module';

export const ssh: KindModule = {
  defaultSettings: createDefaultSshInboundSettings,
  clientSchema: SshClientSchema,
  transports: () => TCP,
  multiUser: true,
  shareLink: false,
};
