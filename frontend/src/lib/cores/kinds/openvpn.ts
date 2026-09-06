import { createDefaultOpenvpnInboundSettings } from '@/lib/xray/inbound-defaults';
import { OpenvpnClientSchema } from '@/schemas/protocols/inbound';

import { TCP, UDP, type KindModule } from './module';

export const openvpn: KindModule = {
  defaultSettings: createDefaultOpenvpnInboundSettings,
  clientSchema: OpenvpnClientSchema,
  transports: (settings) => (settings?.proto === 'tcp' ? TCP : UDP),
  multiUser: true,
  shareLink: false,
};
