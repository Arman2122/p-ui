/*
xray-core v26.9.9 stopped reading finalmask.quicParams.udpHop and hops through
a "udphop" UDP mask instead. intervalLocal + intervalRemote is what the old key
did. Mirrors internal/util/link.MigrateQuicUdpHop, which the startup seeder and
the Go link importer run; this copy serves the form and the browser importer.
*/
export const UDPHOP_LEGACY_MODE = 'intervalLocal,intervalRemote';

function rangeString(v: unknown): string {
  if (typeof v === 'string') return v;
  if (typeof v === 'number' && Number.isFinite(v)) return String(Math.trunc(v));
  return '';
}

export function migrateQuicUdpHop(finalmask: Record<string, unknown>): boolean {
  const quicParams = finalmask.quicParams;
  if (!quicParams || typeof quicParams !== 'object' || Array.isArray(quicParams)) return false;
  const params = quicParams as Record<string, unknown>;
  if (!('udpHop' in params)) return false;
  const legacy = params.udpHop;
  delete params.udpHop;
  if (Object.keys(params).length === 0) delete finalmask.quicParams;

  const settings: Record<string, unknown> = { mode: UDPHOP_LEGACY_MODE };
  if (legacy && typeof legacy === 'object' && !Array.isArray(legacy)) {
    const hop = legacy as Record<string, unknown>;
    const ports = rangeString(hop.ports);
    if (ports !== '') settings.remotePorts = ports;
    const interval = rangeString(hop.interval);
    if (interval !== '') settings.interval = interval;
  }
  const udp = Array.isArray(finalmask.udp) ? [...finalmask.udp] : [];
  udp.push({ type: 'udphop', settings });
  finalmask.udp = udp;
  return true;
}
