import { z } from 'zod';
import {
  FleetInboundNetworkSchema, FleetNetworkBridgeSchema, FleetNetworkOperationSchema,
  FleetNetworkSpecSchema, FleetRuntimeBaseSchema, NetworkBindingSchema,
  type FleetNetworkAuthorityIdentity, type FleetNetworkOperation, type FleetNetworkSpec,
} from '@/generated/zod';

export const FleetAssetKindSchema = z.enum(['tls-certificate', 'tls-private-key', 'ssh-host-keys', 'openvpn-tls-crypt-v2-server', 'wireguard-private-key']);
export const FleetAssetUploadFormSchema = z.object({ kind: FleetAssetKindSchema });
export type FleetAssetUploadForm = z.infer<typeof FleetAssetUploadFormSchema>;
export const NetworkSectionSchema = z.enum(['inbound', 'native-network', 'ssh-host-keys', 'asset-slot', 'base', 'bridge']);
export type NetworkSection = z.infer<typeof NetworkSectionSchema>;
export const NetworkNameSchema = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/);
export const NetworkPortSchema = z.number().int().min(1).max(65535);
export const NetworkInboundIDSchema = z.number().int().positive();
export const NetworkIPv4Schema = z.ipv4();
export const NetworkListenSchema = z.union([z.literal(''), z.ipv4(), z.ipv6()]);

export const NetworkEditSchema = z.object({
  section: NetworkSectionSchema,
  key: z.string().default(''),
  inboundId: NetworkInboundIDSchema.optional(),
  overrideListen: z.boolean().default(false),
  listen: z.string().max(128).default(''),
  overridePort: z.boolean().default(false),
  port: NetworkPortSchema.nullable().optional(),
  peerIPv4: z.string().default(''),
  gatewayIPv4: z.string().default(''),
  hostKeyMode: z.enum(['generate', 'asset']).default('generate'),
  slot: z.string().default(''),
  kind: FleetAssetKindSchema.default('tls-certificate'),
  json: z.string().max(1 << 20).default('{}'),
});
export type NetworkEdit = z.infer<typeof NetworkEditSchema>;

export function hasAuthenticationScope(identity: FleetNetworkAuthorityIdentity): boolean {
  const instances = identity.inboundInstanceIds;
  return identity.credentialGeneration > 0 && identity.placementRevision > 0 && instances.length > 0
    && new Set(instances).size === instances.length && Object.keys(identity.inboundTags).length === instances.length
    && instances.every(id => Boolean(identity.inboundTags[id]?.trim()));
}

export function networkJSONSchema(section: NetworkSection) {
  const shape = section === 'base' ? FleetRuntimeBaseSchema.strict()
    : section === 'bridge' ? FleetNetworkBridgeSchema.strict() : FleetInboundNetworkSchema.omit({ listen: true, port: true }).strict();
  return z.string().max(1 << 20).transform((value, context) => {
    try {
      return shape.parse(JSON.parse(value));
    } catch {
      context.addIssue({ code: 'custom', message: 'pages.nodes.network.invalidJSON' });
      return z.NEVER;
    }
  });
}

export function networkOperation(input: NetworkEdit): FleetNetworkOperation {
  const value = NetworkEditSchema.parse(input);
  const operation: FleetNetworkOperation = { section: value.section, operation: 'replace' };
  switch (value.section) {
    case 'inbound': {
      operation.inboundId = NetworkInboundIDSchema.parse(value.inboundId);
      const inbound = FleetInboundNetworkSchema.strict().parse(networkJSONSchema('inbound').parse(value.json));
      delete inbound.listen;
      delete inbound.port;
      if (value.overrideListen) inbound.listen = value.listen;
      if (value.overridePort) inbound.port = NetworkPortSchema.parse(value.port);
      operation.inbound = inbound;
      break;
    }
    case 'native-network':
      operation.nativeNetwork = NetworkBindingSchema.parse({ peerIPv4: NetworkIPv4Schema.parse(value.peerIPv4), gatewayIPv4: NetworkIPv4Schema.parse(value.gatewayIPv4) });
      break;
    case 'ssh-host-keys':
      operation.sshHostKeys = value.hostKeyMode === 'generate' ? { generateLocally: true } : { generateLocally: false, assetSlot: NetworkNameSchema.parse(value.slot) };
      break;
    case 'asset-slot':
      operation.key = NetworkNameSchema.parse(value.key);
      operation.assetKind = value.kind;
      break;
    case 'base':
      operation.key = NetworkNameSchema.parse(value.key);
      operation.base = FleetRuntimeBaseSchema.strict().parse(networkJSONSchema('base').parse(value.json));
      break;
    case 'bridge':
      operation.key = NetworkNameSchema.parse(value.key);
      operation.bridge = FleetNetworkBridgeSchema.strict().parse(networkJSONSchema('bridge').parse(value.json));
      break;
  }
  return FleetNetworkOperationSchema.parse(operation);
}

export function defaultsOperation(current: FleetNetworkSpec, operation: FleetNetworkOperation): FleetNetworkSpec {
  const result = structuredClone(current);
  const remove = operation.operation !== 'replace';
  switch (operation.section) {
    case 'native-network': result.nativeNetwork = remove ? undefined : operation.nativeNetwork; break;
    case 'ssh-host-keys': result.sshHostKeys = remove ? undefined : operation.sshHostKeys; break;
    case 'inbound':
      result.inbounds ??= {};
      if (remove) delete result.inbounds[String(operation.inboundId)];
      else if (operation.inbound) result.inbounds[String(operation.inboundId)] = operation.inbound;
      break;
    case 'asset-slot':
      result.assetSlots ??= {};
      if (remove) delete result.assetSlots[operation.key ?? ''];
      else if (operation.assetKind) result.assetSlots[operation.key ?? ''] = operation.assetKind;
      break;
    case 'base':
      result.bases ??= {};
      if (remove) delete result.bases[operation.key ?? ''];
      else if (operation.base) result.bases[operation.key ?? ''] = operation.base;
      break;
    case 'bridge':
      result.bridges ??= {};
      if (remove) delete result.bridges[operation.key ?? ''];
      else if (operation.bridge) result.bridges[operation.key ?? ''] = operation.bridge;
      break;
    default: throw new Error('Unsupported defaults section');
  }
  return FleetNetworkSpecSchema.parse(result);
}

export function networkEdit(section: NetworkSection, key: string, spec: FleetNetworkSpec): NetworkEdit {
  const inbound = spec.inbounds?.[key];
  const advancedInbound = { ...inbound };
  delete advancedInbound.listen;
  delete advancedInbound.port;
  const json = section === 'base' ? spec.bases?.[key] ?? { core: 'xray', configuration: {} }
    : section === 'bridge' ? spec.bridges?.[key] ?? { runtimeId: 'xray', core: 'xray', endpoint: '127.0.0.1:1080', udp: false }
      : advancedInbound;
  return NetworkEditSchema.parse({
    section, key, inboundId: section === 'inbound' && key ? Number(key) : undefined,
    overrideListen: inbound?.listen != null, listen: inbound?.listen ?? '', overridePort: inbound?.port != null, port: inbound?.port,
    peerIPv4: spec.nativeNetwork?.peerIPv4 ?? '169.254.234.2', gatewayIPv4: spec.nativeNetwork?.gatewayIPv4 ?? '169.254.234.3',
    hostKeyMode: spec.sshHostKeys?.generateLocally === false ? 'asset' : 'generate', slot: spec.sshHostKeys?.assetSlot ?? '',
    kind: spec.assetSlots?.[key] ?? 'tls-certificate', json: JSON.stringify(json, null, 2),
  });
}
