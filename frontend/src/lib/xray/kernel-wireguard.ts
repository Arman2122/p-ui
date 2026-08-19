/*
The protocols the panel serves as a real kernel WireGuard device.

One predicate rather than a comparison repeated at each site: AmneziaWG is the
same device with obfuscation added, so every question the UI asks about a
wgkernel inbound — does it hand out a .conf, is it UDP, does it have a client
pool, does it show a QR — has the same answer for both. Thirteen separate
comparisons is how one of them gets missed, which is exactly how adding this
protocol first showed up: an inbound the panel would not let you create.
*/
export const KERNEL_WIREGUARD_PROTOCOLS = ['wgkernel', 'awgkernel'] as const;

export function isKernelWireguard(protocol: string | undefined | null): boolean {
  return protocol === 'wgkernel' || protocol === 'awgkernel';
}

// isWireguardLike also covers Xray's own userspace tunnel, for the questions
// that are about WireGuard the protocol rather than about a kernel device.
export function isWireguardLike(protocol: string | undefined | null): boolean {
  return protocol === 'wireguard' || isKernelWireguard(protocol);
}
