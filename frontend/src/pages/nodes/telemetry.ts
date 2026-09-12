import type { NodeRecord } from '@/schemas/node';

export function nodeHasReadings(node: NodeRecord, now = Date.now()): boolean {
  return node.metricsAvailable === true && !node.transitive && !!node.enable && node.status === 'online' &&
    !!node.lastHeartbeat && now / 1000 - node.lastHeartbeat < 90;
}

export function nodeTraffic(node: NodeRecord): number | undefined {
  return node.servedInbounds == null ? undefined : node.servedInbounds.reduce((total, inbound) => total + inbound.up + inbound.down, 0);
}
