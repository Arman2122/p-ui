const PROTOCOL_TONES: Record<string, string> = {
  vless: 'blue', vmess: 'geekblue', trojan: 'volcano', shadowsocks: 'magenta', hysteria: 'cyan',
  hysteria2: 'green', wireguard: 'gold', wgkernel: 'gold', http: 'purple', mixed: 'lime', tunnel: 'orange',
  mtproto: 'purple', awgkernel: 'gold', openvpn: 'cyan', ocserv: 'blue', ssh: 'default',
};

export const inboundTone = (protocol?: string) => protocol ? PROTOCOL_TONES[protocol] : undefined;
