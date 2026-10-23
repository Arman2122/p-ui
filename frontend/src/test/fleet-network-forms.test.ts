import { expect, it } from 'vitest';
import { defaultsOperation, networkEdit, networkOperation } from '@/schemas/forms/fleet-network';
import { FleetNetworkSpecSchema, FleetNodeNetworkStateViewSchema } from '@/generated/zod';

it('distinguishes an explicitly empty listen override from inheritance while preserving advanced fields', () => {
  const spec = FleetNetworkSpecSchema.parse({ version: 1, inbounds: { '42': { listen: '127.0.0.1', port: 443, egressMode: 'xray', bridgeId: 'owned' } } });
  const initial = networkEdit('inbound', '42', spec);
  const inherited = networkOperation({ ...initial, overrideListen: false, overridePort: false });
  expect(inherited).toEqual({ section: 'inbound', operation: 'replace', inboundId: 42, inbound: { egressMode: 'xray', bridgeId: 'owned' } });
  const empty = networkOperation({ ...initial, listen: '', overrideListen: true });
  expect(empty.inbound).toEqual({ egressMode: 'xray', bridgeId: 'owned', listen: '', port: 443 });
  expect(spec.inbounds?.['42']?.listen).toBe('127.0.0.1');
  expect(() => networkOperation({ ...initial, json: '{"listen":"0.0.0.0"}' })).toThrow();
});

it('removes only the selected defaults section without mutating the server snapshot', () => {
  const original = FleetNetworkSpecSchema.parse({ version: 1, inbounds: { '42': { port: 443 } }, nativeNetwork: { peerIPv4: '172.31.240.2', gatewayIPv4: '172.31.240.3' } });
  const updated = defaultsOperation(original, { section: 'inbound', inboundId: 42, operation: 'reset' });
  expect(updated.inbounds).toEqual({});
  expect(updated.nativeNetwork).toEqual(original.nativeNetwork);
  expect(original.inbounds?.['42']?.port).toBe(443);
});

it('accepts actual JSON object inbound keys in a complete node networking read', () => {
  const revision = { id: '', revision: 0, sha256: '', createdUnixMilli: 0 };
  const value = JSON.parse('{"42":{"port":443}}') as unknown;
  const result = FleetNodeNetworkStateViewSchema.parse({ nodeId: 'node', installationId: 'installation', configured: true, revision, defaults: revision, followDefaults: true, pending: true, origins: {}, effective: { version: 1, inbounds: value }, overrides: { version: 1, followDefaults: true, inbounds: value } });
  expect(result.effective.inbounds?.['42']?.port).toBe(443);
});
