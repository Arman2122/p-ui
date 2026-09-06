import { createDefaultTunInboundSettings } from '@/lib/xray/inbound-defaults';

import { NONE, type KindModule } from './module';

export const tun: KindModule = {
  defaultSettings: createDefaultTunInboundSettings,
  clientSchema: null,
  /* A kernel interface binds no port: the stored one is a form field. */
  transports: () => NONE,
  multiUser: false,
  shareLink: false,
};
