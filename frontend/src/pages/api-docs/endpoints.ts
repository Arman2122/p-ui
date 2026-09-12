export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'WS';
export type ParamLocation =
  | 'path'
  | 'query'
  | 'header'
  | 'body'
  | 'body (form)'
  | 'body (json)'
  | 'body (multipart)';
export type ParamType =
  | 'string'
  | 'integer'
  | 'integer[]'
  | 'string[]'
  | 'number'
  | 'boolean'
  | 'object'
  | 'object[]'
  | 'array'
  | 'file';

export interface EndpointParam {
  name: string;
  in: ParamLocation;
  type: ParamType;
  desc?: string;
  optional?: boolean;
  defaultValue?: string | number | boolean;
}

export interface Endpoint {
  method: HttpMethod;
  path: string;
  summary: string;
  description?: string;
  deprecated?: boolean;
  params?: EndpointParam[];
  body?: string;
  response?: string;
  errorResponse?: string;
  errorStatus?: number;
  responseSchema?: string;
  responseSchemaArray?: boolean;
  responseContent?: Record<string, { schema: Record<string, unknown> }>;
  responseErrorEnvelope?: boolean;
  requestSchema?: string;
  requestSchemaArray?: boolean;
  requestBodyRequired?: boolean;
  requestRequired?: string[];
  requestProperties?: Record<string, Record<string, unknown>>;
}

export interface SubscriptionHeader {
  name: string;
  desc: string;
}

export interface Section {
  id: string;
  title: string;
  description?: string;
  subHeader?: SubscriptionHeader[];
  endpoints: Endpoint[];
}

const clientCreateRequestProperties: Record<string, Record<string, unknown>> = {
  client: { $ref: '#/components/schemas/Client', required: ['email'], properties: { totalGB: { description: 'Total traffic quota in bytes; 0 is unlimited.' } } },
  inboundIds: { type: 'array', items: { type: 'integer' }, description: 'Inbound IDs; a plan or group can supply them when omitted.' },
  policyId: { type: 'integer', nullable: true },
  planOverrides: { allOf: [{ $ref: '#/components/schemas/PlanOverrides' }], nullable: true },
  externalLinks: { type: 'array', items: { $ref: '#/components/schemas/ExternalLinkInput', required: [] } },
};

const bulkSelectionProperties: Record<string, Record<string, unknown>> = {
  emails: { type: 'array', items: { type: 'string' }, description: 'Explicit selection; a nonempty list takes precedence over filter.' },
  filter: { type: 'object', additionalProperties: true, description: 'ClientPageParams fields from list/paged. Matching selection is capped at 20000; explicit email lists are not truncated.' },
};

export const sections: readonly Section[] = [
  {
    id: 'authentication',
    title: 'Authentication',
    description:
      'UI sessions use a cookie set by the login endpoint. Programmatic clients authenticate with a Bearer token from Panel Settings → API Token or their reseller account. Each operation enforces the authenticated role, token scope and ownership. The global WebSocket requires an administrator session cookie.',
    endpoints: [
      {
        method: 'POST',
        path: '/login',
        errorStatus: 200,
        summary: 'Authenticate with username + password and receive a session cookie. Required before any cookie-based API call.',
        params: [ { name: 'username', in: 'body', type: 'string', desc: 'Panel admin username.' },
  { name: 'password', in: 'body', type: 'string', desc: 'Panel admin password.' },
  { name: 'twoFactorCode', in: 'body', type: 'string', desc: 'OTP code when 2FA is enabled. Omit otherwise.', optional: true } ],
        body: '{\n  "username": "admin",\n  "password": "admin",\n  "twoFactorCode": "123456"\n}',
        response:
          '{\n  "success": true,\n  "msg": "Logged in successfully"\n}',
        errorResponse:
          '{\n  "success": false,\n  "msg": "Wrong username or password"\n}',
      },
      {
        method: 'POST',
        path: '/logout',
        summary: 'Clear the session cookie. Requires the CSRF header for browser sessions.',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'GET',
        path: '/csrf-token',
        summary: 'Mint a CSRF token for the current session. The SPA replays it in the X-CSRF-Token header on unsafe requests. Bearer-token callers can skip this — the middleware short-circuits CSRF for authenticated API requests.',
        response:
          '{\n  "success": true,\n  "obj": "csrf-token-string"\n}',
      },
      {
        method: 'POST',
        path: '/getTwoFactorEnable',
        summary: 'Returns whether 2FA is enabled on the panel — used by the login page to decide whether to show the OTP field.',
        response: '{\n  "success": true,\n  "obj": false\n}',
      },
    ],
  },

  {
    id: 'inbounds',
    title: 'Inbounds',
    description:
      'Manage inbound configurations and their clients. All endpoints live under /panel/api/inbounds and require a logged-in session or Bearer token. Link-generating endpoints honour forwarded headers only when the request comes from a configured trusted proxy.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/inbounds/list',
        summary: 'List every inbound owned by the authenticated user, including each inbound’s clientStats traffic counters. settings, streamSettings, and sniffing are returned as nested JSON objects (no escaped strings); legacy callers that send them back as JSON-encoded strings are still accepted on write.',
        responseSchema: 'Inbound',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/list/slim',
        summary: 'Same shape as /list but with settings.clients[] stripped down to {email, enable, comment} and ClientStats not enriched with UUID/SubId. Use this for list pages; fetch /get/:id when you need the full per-client payload (uuid, password, flow, ...).',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "remark": "VLESS-443",\n      "settings": {\n        "clients": [\n          { "email": "alice", "enable": true }\n        ],\n        "decryption": "none"\n      },\n      "clientStats": []\n    }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/options',
        summary: 'Lightweight picker projection of the authenticated user’s inbounds. Returns id, remark, tag, protocol, port, a server-computed tlsFlowCapable flag (true for VLESS on TCP with tls or reality, or on XHTTP with VLESS encryption / vlessenc enabled), and ssMethod (the Shadowsocks cipher, empty for non-Shadowsocks inbounds — used by the client UI to generate a valid Shadowsocks 2022 PSK). Use this for dropdowns and attach pickers — it skips settings, streamSettings, and clientStats so the payload stays small even on panels with thousands of clients.',
        responseSchema: 'InboundOption',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/allLinks',
        summary:
          'Return every protocol URL (vless://, vmess://, trojan://, ss://, hysteria://, mtproto) across all inbounds and all of their clients. Links are rendered through the subscription engine, so the configured remark template (name-only display part) is applied per client — the same output the client info/QR pages use. Protocols without a URL form (socks, http, mixed, wireguard, dokodemo, tunnel) contribute nothing. Used by the panel’s "Export all inbound links" action.',
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?security=reality&...#Germany-alice",\n    "vmess://eyJ2IjoyLC..."\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/:id/links',
        summary:
          'Return one inbound’s protocol URLs, rendered through the subscription engine so the inbound’s hosts are applied — the same links a subscriber receives. Pass ?email= to narrow the answer to a single client. This is what the panel’s QR, inbound info and “Export links” surfaces call; the browser cannot render these itself because it has no concept of a Host row. Protocols without a URL form (socks, http, mixed, dokodemo, tunnel) contribute nothing.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
          { name: 'email', in: 'query', type: 'string', desc: 'Optional. Limit the links to this client’s.' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@cdn.example.com:2096?security=tls&...#Germany-alice"\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/get/:id',
        summary: 'Fetch a single inbound by numeric ID.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/add',
        summary: 'Create a new inbound. Send the full inbound payload (protocol, port, settings, streamSettings, sniffing, remark, expiryTime, total, enable). settings, streamSettings, and sniffing may be sent as nested JSON objects (preferred) or as JSON-encoded strings (legacy). An inbound for a core this host cannot run (its preflight fails, e.g. a kernel module is missing) is refused with the core\'s reason; Xray inbounds keep their own -test pre-flight at restart.',
        body:
          '{\n  "enable": true,\n  "remark": "VLESS-443",\n  "listen": "",\n  "port": 443,\n  "protocol": "vless",\n  "expiryTime": 0,\n  "total": 0,\n  "settings": {\n    "clients": [{ "id": "...", "email": "user1" }],\n    "decryption": "none",\n    "fallbacks": []\n  },\n  "streamSettings": {\n    "network": "tcp",\n    "security": "reality",\n    "realitySettings": { "show": false, "dest": "..." }\n  },\n  "sniffing": {\n    "enabled": true,\n    "destOverride": ["http", "tls"]\n  }\n}',
        errorResponse:
          '{\n  "success": false,\n  "msg": "Port 443 is already in use"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/del/:id',
        summary: 'Delete an inbound by ID. Also removes its associated client stats rows. An id that does not exist answers not-found instead of success.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/bulkDel',
        summary: 'Delete many inbounds in one call. Processes the list sequentially; failures are reported per id and the rest still proceed. Restarts xray at most once.',
        body: '{\n  "ids": [1, 2, 3]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 2,\n    "skipped": [\n      { "id": 3, "reason": "..." }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/bulkSetEnable',
        summary: 'Enable or disable many inbounds in one call. Each inbound is flipped the way setEnable/:id does (hot-added or removed on a local core, pushed to its node); an inbound already in the requested state, an unknown id, or one the core refuses is reported per id and the rest still proceed. Restarts are flagged once for the batch.',
        body: '{\n  "ids": [1, 2, 3],\n  "enable": false\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "changed": 2,\n    "skipped": [\n      { "id": 3, "reason": "already disabled" }\n    ]\n  }\n}',
      },
      {
  requestSchema: 'Inbound',
  requestRequired: [],

        method: 'POST',
        path: '/panel/api/inbounds/update/:id',
        summary: 'Replace an inbound’s configuration. Body shape mirrors /add. Heavy on inbounds with thousands of clients — prefer /setEnable for enable-only flips.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/setEnable/:id',
        summary: 'Toggle only the enable flag without serialising the whole settings JSON. Recommended for UI switches on large inbounds.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
        body: '{\n  "enable": false\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/resetTraffic',
        summary: 'Zero out upload + download counters for a single inbound. Does not touch per-client counters.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/delAllClients',
        summary: 'Remove every client attached to a single inbound while keeping the inbound itself. Collects emails from settings.clients[] and feeds them into the optimized bulk-delete path (runtime user removal + traffic-row cleanup + SyncInbound). Destructive and cannot be undone.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 12\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/resetAllTraffics',
        summary: 'Reset upload + download counters on every inbound. Destructive — accounting history is lost.',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/import',
        summary: 'Bulk-import an inbound from a JSON blob (e.g. one exported via the UI). The body uses form encoding with a single "data" field.',
        params: [
          { name: 'data', in: 'body (form)', type: 'string', desc: 'JSON-encoded inbound payload.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/pushClientTraffics',
        summary: 'Receive a master panel\'s aggregated per-client usage, keyed by the master\'s GUID. Stored in a side table used only for the UI display overlay and local quota enforcement — never folded into the local counters that masters poll, so delta accounting stays intact. Called panel-to-panel by the node traffic sync job.',
        params: [
          { name: 'masterGuid', in: 'body (json)', type: 'string', desc: 'Stable GUID of the pushing master panel.' },
          { name: 'traffics', in: 'body (json)', type: 'object[]', desc: 'Client traffic rows; only email/up/down are read.' },
        ],
        body: '{\n  "masterGuid": "9f6c2d-…",\n  "traffics": [\n    { "email": "alice", "up": 1048576, "down": 2097152 }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/:id/fallbacks',
        summary: 'List the fallback rules attached to a master VLESS/Trojan TCP-TLS inbound. Each rule links one child inbound (the dest) to optional SNI/ALPN/path/dest/xver match criteria. When dest is empty the child inbound\'s listen+port is used.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Master inbound ID.' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "masterId": 10,\n      "childId": 11,\n      "name": "",\n      "alpn": "",\n      "path": "/vlws",\n      "dest": "",\n      "xver": 2,\n      "sortOrder": 0\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/fallbacks',
        summary: 'Replace the entire fallback list for a master inbound. Body is JSON. Triggers an Xray restart.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Master inbound ID.' },
          { name: 'fallbacks', in: 'body (json)', type: 'object[]', desc: 'Array of {childId, name, alpn, path, dest, xver, sortOrder} entries. Leave dest empty to auto-resolve from the child inbound\'s listen+port; set it (e.g. "8443", "127.0.0.1:8443", "/dev/shm/x.sock") to override.' },
        ],
        body: '{\n  "fallbacks": [\n    { "childId": 11, "path": "/vlws", "xver": 2 },\n    { "childId": 12, "alpn": "h2", "dest": "8443" }\n  ]\n}',
        response: '{\n  "success": true,\n  "msg": "Inbound updated"\n}',
      },
    ],
  },

  {
    id: 'server',
    title: 'Server',
    description:
      'System status, log retrieval, certificate generators, Xray binary management, and backup/restore. All under /panel/api/server.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/openapi.json',
        summary: 'Serve this API description as an OpenAPI 3 document — the same file that powers the API Docs page. Requires a session or Bearer token like the rest of /panel/api. Useful for generating clients or importing into API tooling.',
        responseContent: { 'application/json': { schema: { type: 'object', additionalProperties: true } } },
      },
      {
        method: 'GET',
        path: '/panel/api/cores',
        summary: 'List the protocol cores this build can serve, each with the inbound kinds it handles and the capabilities it declares. An id is not a protocol — one core serves many kinds. A null capability means the core did not answer; treat it as unknown, never as false.',
        responseSchema: 'CoreView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/cores/:id/restart',
        summary: 'Restart one core’s daemons: stop them, then converge them back on the inbounds they should be serving. The id is a core id from GET /panel/api/cores, never a protocol — one core serves many kinds. Xray is converged through its generated config rather than an instance set, so it delegates to the same path /restartXrayService uses. A core that runs no daemon this panel supervises is refused.',
        params: [{ name: 'id', in: 'path', type: 'string', desc: 'Core id, e.g. mtproto.' }],
      },
      {
        method: 'GET',
        path: '/panel/api/cores/status',
        summary: 'How each core is doing right now, from the daemon’s side: state (running, degraded, stopped, failed, idle or unknown), whether it answers on its control surface, pid, supervisor restarts, since when, the last exit or the reason it is not ready, per inbound for a core that runs one daemon each, plus the installed version and whether the panel may change it. Polled by the Cores page; GET /panel/api/cores is the static half.',
        responseSchema: 'CoreStatusView',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/cores/:id/versions',
        summary: 'The build of this core’s daemon on disk and, for a core that can install one, the releases its own channel publishes. The list is cached for 15 minutes and a stale list is served over a failed refresh. A core that names no version (SSH is the panel itself) is refused.',
        params: [{ name: 'id', in: 'path', type: 'string', desc: 'Core id from GET /panel/api/cores.' }],
        responseSchema: 'CoreVersionsView',
      },
      {
        method: 'POST',
        path: '/panel/api/cores/:id/install/:version',
        summary: 'Download, verify and install one listed release of this core’s daemon. The binary is staged beside the installed one, checked (a digest or checksum the channel publishes, and for Xray whether the new binary loads this panel’s config), then renamed into place. Nothing is restarted: the running daemon keeps the old binary until POST /cores/:id/restart. A version the channel does not list is refused. Only cores that manage their own releases accept this — Xray and MTProto today.',
        params: [
          { name: 'id', in: 'path', type: 'string', desc: 'Core id from GET /panel/api/cores.' },
          { name: 'version', in: 'path', type: 'string', desc: 'A tag from GET /panel/api/cores/:id/versions, e.g. v26.7.28.' },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/cores/:id/logs',
        summary: 'The last lines a core’s daemon wrote: the journal under systemd, the panel’s own ring under exec, kept across the restart that repairs a crash. For a core that runs one daemon per inbound pass ?instance=<inbound id>; a single-process core takes none. A core that keeps no log the panel can read is refused.',
        params: [
          { name: 'id', in: 'path', type: 'string', desc: 'Core id from GET /panel/api/cores.' },
          { name: 'instance', in: 'query', type: 'number', desc: 'Inbound id for a per-inbound core. Defaults to 0, the whole of a single-process core.' },
          { name: 'n', in: 'query', type: 'number', desc: 'Lines to return, newest last. Defaults to 200, capped at 1000.' },
        ],
        response: '{\n  "success": true,\n  "obj": [\n    "2026-09-05T18:33:49+0000 ocserv[2044848]: listening (TCP) on 127.0.0.1:8443..."\n  ]\n}',
      },
      {
method: 'GET',
path: '/panel/api/server/status',
summary: 'Real-time machine snapshot: CPU, memory, swap, disk, network IO, load averages, open connections, Xray state. Cached and refreshed every 2 seconds in the background.',
responseSchema: 'Status',
},
      {
        method: 'GET',
        path: '/panel/api/server/ipLimitStatus',
        summary: 'Reports whether a device over its cap can be dropped on this host. enabled is the PUI_IP_LIMIT_BANS toggle; enforceable is whether the panel can write its own firewall ban table (nft present, permission to write it), and reason says why not. The client form uses it to gate the "IP Limit" field for kinds the firewall enforces.',
        responseSchema: 'IpLimitStatus',
      },
      {
        method: 'GET',
        path: '/panel/api/server/cpuHistory/:bucket',
        summary: 'Legacy: aggregated CPU history. Use /history/cpu/:bucket instead — same data with a uniform {t, v} shape.',
        params: [
          { name: 'bucket', in: 'path', type: 'number', desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.' },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/history/:metric/:bucket',
        summary: 'Aggregated time-series for one metric. Returns an array of {t, v} samples covering the last ~6 hours.',
        params: [
          { name: 'metric', in: 'path', type: 'string', desc: 'cpu | mem | netUp | netDown | online | load1 | load5 | load15.' },
          { name: 'bucket', in: 'path', type: 'number', desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.' },
        ],
        response: '{\n  "success": true,\n  "obj": [\n    { "t": 1700000000, "v": 12.5 },\n    { "t": 1700000002, "v": 13.1 }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayMetricsState',
        summary: 'Xray runtime metrics state — whether the xray config has a `metrics` block, which expvar keys are flowing, and the current snapshot values for each. Returns an empty state when metrics are not configured.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayMetricsHistory/:metric/:bucket',
        summary: 'Time-series history for one Xray runtime metric over the last ~6 hours. Same {t, v} shape as /history/:metric/:bucket.',
        params: [
          { name: 'metric', in: 'path', type: 'string', desc: 'xrAlloc | xrSys | xrHeapObjects | xrNumGC | xrPauseNs.' },
          { name: 'bucket', in: 'path', type: 'number', desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.' },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayObservatory',
        summary: 'Latest snapshot from the Xray observatory — per-outbound latency, health status, and last-probe time. Only populated when the Xray config has an observatory configured.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayObservatoryHistory/:tag/:bucket',
        summary: 'Time-series of observatory probe results for one outbound tag. Same {t, v} shape as the other history endpoints.',
        params: [
          { name: 'tag', in: 'path', type: 'string', desc: 'Outbound tag from the observatory config.' },
          { name: 'bucket', in: 'path', type: 'number', desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.' },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/getXrayVersion',
        summary: 'List Xray binary versions available for install on this host.',
        response: '{\n  "success": true,\n  "obj": ["v25.10.31", "v25.9.15", "v25.8.1"]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getPanelUpdateInfo',
        summary: 'Check whether a newer Penhoon UI release is available on GitHub.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getUpdateStatus',
        summary: 'Report the outcome of the most recently launched panel self-update (see POST updatePanel). Compare the returned runId against the one updatePanel returned to tell this run apart from a stale result.',
        responseSchema: 'PanelUpdateStatus',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getConfigJson',
        summary: 'Return the assembled Xray config that\u2019s currently running on this host.',
        response: '{\n  "success": true,\n  "obj": {\n    "log": { "loglevel": "warning" },\n    "inbounds": [...],\n    "outbounds": [...],\n    "routing": { "rules": [...] }\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getDb',
        summary: 'Stream a full database backup as an attachment — a pg_dump custom-format archive (.dump). Use as a manual backup.',
        responseContent: { 'application/octet-stream': { schema: { type: 'string', format: 'binary' } } },
        responseErrorEnvelope: true,
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewUUID',
        summary: 'Generate a fresh UUID v4. Convenience helper for client IDs.',
        response: '{\n  "success": true,\n  "obj": "550e8400-e29b-41d4-a716-446655440000"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getWebCertFiles',
        summary: 'Return this panel\'s own web TLS certificate and key file paths. The central panel calls it on a node (via the node API token) so "Set Cert from Panel" fills a node-assigned inbound with paths that exist on the node.',
        response: '{\n  "success": true,\n  "obj": {\n    "webCertFile": "/root/cert/example.com/fullchain.pem",\n    "webKeyFile": "/root/cert/example.com/privkey.pem"\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/descendants',
        summary: 'Read-only summaries (guid, parentGuid, name, address, status, versions) of the nodes this panel manages. A parent panel calls it on a node (via the node API token) to surface transitive sub-nodes in a chained topology. Counts are computed by the parent, not returned here.',
        response: '{\n  "success": true,\n  "obj": [\n    {\n      "guid": "c3d4-...",\n      "parentGuid": "a1b2-...",\n      "name": "Node3",\n      "address": "10.0.0.3",\n      "status": "online"\n    }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewX25519Cert',
        summary: 'Generate a new X25519 keypair for Reality.',
        response: '{\n  "success": true,\n  "obj": {\n    "privateKey": "uN9qLfV3zH8w...",\n    "publicKey": "5v8xPqR2sM7k..."\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewmldsa65',
        summary: 'Generate a new ML-DSA-65 keypair (post-quantum signature). Returns {privateKey, publicKey, seed}.',
        response: '{\n  "success": true,\n  "obj": {\n    "privateKey": "mdsa65priv...",\n    "publicKey": "mdsa65pub...",\n    "seed": "random-seed..."\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewmlkem768',
        summary: 'Generate a new ML-KEM-768 keypair (post-quantum KEM). Returns {clientKey, serverKey}.',
        response: '{\n  "success": true,\n  "obj": {\n    "clientKey": "mlkem768-client...",\n    "serverKey": "mlkem768-server..."\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewVlessEnc',
        summary: 'Generate VLESS encryption auth options. Returns an auths array each with id, label, encryption, and decryption fields.',
        response: '{\n  "success": true,\n  "obj": {\n    "auths": [\n      { "id": 0, "label": "Auth #0", "encryption": "aes-256-gcm", "decryption": "" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/stopXrayService',
        summary: 'Stop the Xray binary. All proxies go offline immediately.',
        errorResponse:
          '{\n  "success": false,\n  "msg": "Xray is not running"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/restartXrayService',
        summary: 'Reload Xray with the current config. Typically required after structural inbound or routing changes.',
        errorResponse:
          '{\n  "success": false,\n  "msg": "Xray config is invalid: ..."\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/installXray/:version',
        summary: 'Download and install the specified Xray version. Pass "latest" for the newest release.',
        params: [
          { name: 'version', in: 'path', type: 'string', desc: 'Xray tag (e.g. v25.10.31) or "latest".' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/server/updatePanel',
        summary: 'Self-update the panel to the latest version. The server restarts on success.',
        response: '{\n  "success": true,\n  "obj": {\n    "runId": "1735689600123456789"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/setUpdateChannel',
        summary: 'Toggle the panel update channel between stable and the rolling per-commit dev release. Only effective on dev builds.',
        params: [
          { name: 'dev', in: 'body (form)', type: 'boolean', desc: 'true = dev channel, false = stable.' },
        ],
        body: 'dev=true',
      },
      {
        method: 'POST',
        path: '/panel/api/server/updateGeofile',
        summary: 'Refresh the default GeoIP / GeoSite data files. Body can include a fileName, or use the /:fileName variant.',
        params: [
          { name: 'fileName', in: 'body (form)', type: 'string', desc: 'Filename to update (e.g. geoip.dat, geosite.dat). Omit to update all defaults.' },
        ],
        body: 'fileName=geoip.dat',
      },
      {
        method: 'POST',
        path: '/panel/api/server/updateGeofile/:fileName',
        summary: 'Refresh a single Geo file by filename (e.g. geoip.dat, geosite.dat).',
        params: [
          { name: 'fileName', in: 'path', type: 'string', desc: 'Filename of the data file to refresh.' },
        ],
      },
      {
  requestBodyRequired: false,

        method: 'POST',
        path: '/panel/api/server/logs/:count',
        summary: 'Return the last N lines of the panel\u2019s own log.',
        params: [ { name: 'count', in: 'path', type: 'number', desc: 'Number of trailing log lines.' },
  { name: 'level', in: 'body (form)', type: 'string', optional: true, desc: 'Log level filter.' },
  { name: 'syslog', in: 'body (form)', type: 'boolean', optional: true, desc: 'Read system journal when true.' } ],
        body: '{\n  "level": "info",\n  "syslog": false\n}',
        response: '{\n  "success": true,\n  "obj": "2025/01/01 12:00:00 [INFO] Server started\\n2025/01/01 12:00:01 [INFO] Xray is running"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/xraylogs/:count',
        summary: 'Return the last N lines of the Xray process log.',
        params: [
          { name: 'count', in: 'path', type: 'number', desc: 'Number of trailing log lines.' },
          { name: 'filter', in: 'body (form)', type: 'string', desc: 'Keyword filter — only lines containing this string.' },
          { name: 'showDirect', in: 'body (form)', type: 'string', desc: '"true" to include direct (freedom) traffic lines.' },
          { name: 'showBlocked', in: 'body (form)', type: 'string', desc: '"true" to include blocked (blackhole) traffic lines.' },
          { name: 'showProxy', in: 'body (form)', type: 'string', desc: '"true" to include proxy traffic lines.' },
        ],
        body: 'filter=error&showDirect=false&showBlocked=true&showProxy=true',
        response: '{\n  "success": true,\n  "obj": "2025/01/01 12:00:00 rejected  vless  proxy  example.com  reason: no valid user\\n2025/01/01 12:00:01 direct  freedom  ok"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/importDB',
        summary: 'Restore the panel DB from an uploaded pg_dump archive (.dump), sent as multipart form field "db" and loaded with pg_restore. The panel restarts after restore. Destructive.',
        params: [
          { name: 'db', in: 'body (multipart)', type: 'file', desc: 'pg_dump archive (.dump) to upload.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/server/getNewEchCert',
        summary: 'Generate a new ECH (Encrypted Client Hello) keypair and config list for the given SNI.',
        params: [
          { name: 'sni', in: 'body (form)', type: 'string', desc: 'Server Name Indication to generate the ECH config for.' },
        ],
        body: 'sni=example.com',
        response: '{\n  "success": true,\n  "obj": {\n    "echKeySet": "...",\n    "echServerKeys": [...],\n    "echConfigList": "..."\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/getCertHash',
        summary: 'Compute the hex SHA-256 of a certificate (DER) for pinning (pinnedPeerCertSha256). Provide either a server file path or inline PEM/DER content.',
        params: [
          { name: 'certFile', in: 'body (form)', type: 'string', desc: 'Path to a certificate file on the server. Takes precedence over certContent.' },
          { name: 'certContent', in: 'body (form)', type: 'string', desc: 'Inline PEM (or DER) certificate content, used when certFile is empty.' },
        ],
        body: 'certFile=/root/cert.crt',
        response: '{\n  "success": true,\n  "obj": [\n    "e8e2d3..."\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/getRemoteCertHash',
        summary: 'Run `xray tls ping` against a remote server and return its live leaf-certificate SHA-256 hash(es) for pinning (pinnedPeerCertSha256).',
        params: [
          { name: 'server', in: 'body (form)', type: 'string', desc: 'Remote server as domain or domain:port (default port 443), e.g. cloudflare-dns.com.' },
        ],
        body: 'server=cloudflare-dns.com',
        response: '{\n  "success": true,\n  "obj": [\n    "e8e2d3..."\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/scanRealityTarget',
        summary: 'Run a live TLS 1.3 probe against a candidate REALITY target and return a feasibility verdict (TLS 1.3 + h2 + X25519 + trusted certificate) plus the certificate SAN DNS names.',
        params: [
          { name: 'target', in: 'body (form)', type: 'string', desc: 'Candidate target as host or host:port (default port 443), e.g. www.cloudflare.com:443.' },
        ],
        body: 'target=www.cloudflare.com:443',
        responseSchema: 'RealityScanResult',
      },
      {
        method: 'POST',
        path: '/panel/api/server/scanRealityTargets',
        summary: 'Probe/discover REALITY targets and return each verdict ranked by feasibility then latency. Each comma-separated token may be a domain (validated with SNI), a bare IP, or a CIDR range (discovered without SNI by reading the certificate domain). When empty, a built-in seed list is probed.',
        params: [
          { name: 'targets', in: 'body (form)', type: 'string', optional: true, desc: 'Optional comma-separated tokens: domain[:port], IP[:port], or CIDR (e.g. 104.16.0.0/24). When omitted, a built-in seed list is probed.' },
        ],
        body: 'targets=104.16.0.0/24,www.apple.com:443',
        responseSchema: 'RealityScanResult',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/server/clientIps',
        summary: 'Fetch the fully aggregated inbound_client_ips database table. Used by nodes to sync recently active IPs across the cluster.',
        responseSchema: 'InboundClientIps',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/server/clientIps',
        summary: 'Submit a list of recently active IP timestamps. The panel merges them with the existing database to maintain a unified global IP-limit view.',
        params: [
          { name: 'ips', in: 'body (json)', type: 'object[]', desc: 'Array of InboundClientIps to merge.' },
        ],
      },

{
  method: 'GET',
  path: '/panel/api/server/historyWindow/:bucket',
  summary: 'Batch host history with a shared epoch-second axis and up to 60 buckets; 15-second buckets cover 15 minutes.',
  responseSchema: 'MetricHistoryWindow',
  params: [
    {
      name: 'bucket',
      in: 'path',
      type: 'integer',
      desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.'
    }
  ]
},
],
  },

  {
    id: 'clients',
    title: 'Clients',
    description:
      'Manage clients as first-class entities that can be attached to one or more inbounds. A single client row drives the settings.clients entry in every inbound it belongs to. Endpoints live under /panel/api/clients.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/clients/list',
        summary: 'List every client with its attached inbound IDs and traffic record. The reverse field, if set, is returned as a nested JSON object (legacy JSON-encoded-string form is still accepted on write).',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "email": "alice@example.com",\n      "subId": "abcd1234",\n      "uuid": "...",\n      "totalGB": 53687091200,\n      "expiryTime": 1735689600000,\n      "enable": true,\n      "reverse": null,\n      "inboundIds": [3, 5],\n      "traffic": { "up": 1024, "down": 4096, "enable": true }\n    }\n  ]\n}',
      },
      {
method: 'GET',
path: '/panel/api/clients/list/paged',
summary: 'Filter, sort, and paginate clients on the server. Each item is a slim row (no uuid/password/auth/flow/security/reverse/tgId) so the clients page can ship 25-ish rows in a few KB instead of the full table. The response also includes a summary computed across the full DB row set so dashboard counters stay stable as the user paginates or filters: the *Count fields are exact, while the email arrays beside them stop at 200 entries so the payload does not grow with the panel. Page size capped at 200; fetch /get/:email to obtain the full per-client payload for an edit/info modal.',
params: [
          { name: 'page', in: 'query', type: 'number', desc: '1-indexed page number. Defaults to 1.' },
          { name: 'pageSize', in: 'query', type: 'number', desc: 'Rows per page. Defaults to 25, capped at 200.' },
          { name: 'search', in: 'query', type: 'string', desc: 'Case-insensitive substring match on email / subId / comment.' },
          { name: 'filter', in: 'query', type: 'string', desc: 'Status bucket: online | active | deactive | depleted | expiring.' },
          { name: 'protocol', in: 'query', type: 'string', desc: 'Match clients attached to at least one inbound of this protocol (vless, vmess, trojan, shadowsocks, ...).' },
          { name: 'sort', in: 'query', type: 'string', desc: 'Sort key: enable | email | inboundIds | traffic | remaining | expiryTime.' },
          { name: 'order', in: 'query', type: 'string', desc: 'ascend or descend.' },
        ],
responseSchema: 'ClientPageResponse',
},
      {
        method: 'GET',
        path: '/panel/api/clients/get/:email',
        summary: 'Fetch one client by email, including the inbound IDs and external config IDs it is attached to.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": {\n    "client": { "id": 1, "email": "alice@example.com", ... },\n    "credentials": { "secret": "ee...", "adTag": "0123..." },\n    "inboundIds": [3, 5],\n    "externalLinks": [{ "kind": "link", "value": "vless://...", "remark": "DE" }]\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/get/tgId/:tgId',
        summary: 'Fetch clients by Telegram user ID. Returns an array since multiple clients can share the same Telegram ID.',
        params: [
          { name: 'tgId', in: 'path', type: 'integer', desc: 'Telegram user ID (numeric).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "client": { "id": 1, "email": "alice@example.com", ... },\n      "credentials": { "secret": "ee...", "adTag": "0123..." },\n      "inboundIds": [3, 5],\n      "externalLinks": [],\n      "usedTraffic": 1048576\n    }\n  ]\n}',
      },
      {
  requestBodyRequired: true,
  requestProperties: clientCreateRequestProperties,
  requestRequired: [ 'client' ],

method: 'POST',
path: '/panel/api/clients/add',
summary: 'Create a new client and attach it to one or more inbounds in a single call. Body is JSON. Per-protocol secrets (UUID for VLESS/VMess, password for Trojan/Shadowsocks, auth for Hysteria) are generated server-side when omitted, so callers can send only the universal fields.',
params: [],
body: '{\n  "client": {\n    "email": "alice@example.com",\n    "totalGB": 53687091200,\n    "expiryTime": 1735689600000,\n    "tgId": 0,\n    "limitIp": 0,\n    "enable": true\n  },\n  "inboundIds": [3, 5]\n}',
response: '{\n  "success": true,\n  "msg": "Client added"\n}',
},
      {
  requestSchema: 'Client',
  requestProperties: { totalGB: { description: 'Total traffic quota in bytes; 0 is unlimited.' },  policyId: { type: 'integer', nullable: true },
  planOverrides: { allOf: [ { '$ref': '#/components/schemas/PlanOverrides' } ], nullable: true },
  inboundIds: { type: 'array', items: { type: 'integer' }, nullable: true } },

method: 'POST',
path: '/panel/api/clients/update/:email',
summary: 'Update an existing client by email. Changes propagate to every attached inbound. Body is the JSON client payload — supply the full set of fields you want to keep (the server replaces the row, it does not patch).',
params: [ { name: 'email', in: 'path', type: 'string', desc: 'Current client email (unique identifier).' },
  { name: 'policyId',
    in: 'body',
    type: 'integer',
    optional: true,
    desc: 'Explicit plan assignment; zero unassigns, absence preserves existing behavior.' },
  { name: 'planOverrides',
    in: 'body',
    type: 'object',
    optional: true,
    desc: 'Explicit quota, expiry, limitIp and inbounds override flags; omitted flags are preserved.' },
  { name: 'inboundIds',
    in: 'query',
    type: 'string',
    optional: true,
    desc: 'Optional comma-separated inbound IDs restricting where the client update is applied; distinct from body inboundIds plan override.' } ],
body: '{\n  "email": "alice@example.com",\n  "totalGB": 107374182400,\n  "expiryTime": 1767225600000,\n  "tgId": 123456789,\n  "enable": true\n}',
response: '{\n  "success": true,\n  "msg": "Client updated"\n}',
},
      {
        method: 'POST',
        path: '/panel/api/clients/del/:email',
        summary: 'Delete a client by email. Removes it from every attached inbound and drops its traffic record unless keepTraffic=1 is passed.',
        params: [ { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
  { name: 'keepTraffic', in: 'query', type: 'integer', desc: 'Pass 1 to retain the xray_client_traffic row after deletion.', optional: true } ],
        response: '{\n  "success": true,\n  "msg": "Client deleted"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/attach',
        summary: 'Attach an existing client to one or more additional inbounds. Body is JSON.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          { name: 'inboundIds', in: 'body (json)', type: 'integer[]', desc: 'Inbound IDs to attach.' },
        ],
        body: '{\n  "inboundIds": [7, 9]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/detach',
        summary: 'Detach a client from one or more inbounds without deleting the client.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          { name: 'inboundIds', in: 'body (json)', type: 'integer[]', desc: 'Inbound IDs to detach.' },
        ],
        body: '{\n  "inboundIds": [5]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/externalLinks',
        summary: 'Replace a client\'s external links (per-client share links and remote subscription URLs surfaced in their subscription). Sends the full set; the server replaces all rows.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          { name: 'externalLinks', in: 'body (json)', type: 'object[]', desc: 'Rows of { kind: "link" | "subscription", value, remark }. kind=link must be a share link; kind=subscription must be an http(s) URL.' },
        ],
        body: '{\n  "externalLinks": [\n    { "kind": "link", "value": "vless://uuid@host:443?...#srv", "remark": "DE" },\n    { "kind": "subscription", "value": "https://provider.example/sub/abc", "remark": "Provider" }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/resetAllTraffics',
        summary: 'Reset the up/down counters for every client globally. Quotas and expiry are not affected. Triggers an Xray restart if any counter actually moved.',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/delDepleted',
        summary: 'Delete every client whose traffic quota is exhausted (used >= total, when reset is disabled) or whose expiry has passed. Returns the deleted count and triggers an Xray restart when any client was on a running inbound.',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 0\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/delOrphans',
        summary: 'Delete every client that is not attached to any inbound, along with its traffic record, IP log, and external links. Useful for clearing clients left unattached after their inbounds were removed. Returns the deleted count. Cannot be undone.',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 0\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/export',
        summary: 'Return every client as a {client, inboundIds} array — the same shape /bulkCreate and /import accept — so the payload round-trips straight back through /import. Clients with no inbound attachment are included with an empty inboundIds list. The UI shows this in a CodeMirror viewer (copy / download); programmatic callers get the array in obj. Each item also carries externalLinks (kind link or subscription, value, remark), which /clients/import writes back.',
        response: '{\n  "success": true,\n  "obj": [\n    {\n      "client": {\n        "email": "alice@example.com",\n        "id": "...",\n        "totalGB": 53687091200,\n        "expiryTime": 0,\n        "enable": true,\n        "subId": "..."\n      },\n      "inboundIds": [7, 9]\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/import',
        summary: 'Import clients from a JSON body { "data": "<json>" }, where data is a string-encoded array produced by /export ([{client, inboundIds}]). Items with inboundIds are created and attached to those inbounds; items with an empty inboundIds list are restored as unattached client records. Existing emails are never overwritten — they are returned in skipped. Triggers a single Xray restart at the end if any target inbound was running. An item\'s externalLinks are validated first and restored after the client is created.',
        body: '{\n  "data": "[{\\"client\\":{\\"email\\":\\"alice@example.com\\",\\"enable\\":true},\\"inboundIds\\":[7]}]"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "created": 2,\n    "skipped": [\n      { "email": "alice@example.com", "reason": "email already in use: alice@example.com" }\n    ]\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, addDays: { type: 'integer', description: 'Signed days to add, or days from now in set mode; zero leaves unchanged.' }, addBytes: { type: 'integer', format: 'int64', description: 'Signed bytes to add, or quota bytes in set mode; zero leaves unchanged.' }, flow: { type: 'string' }, mode: { type: 'string', enum: [ 'add', 'set' ], default: 'add' } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkAdjust',
        summary: 'Shift expiry and/or traffic quota for many clients in one call. addDays/addBytes may be negative. Clients with unlimited expiry (expiryTime=0) or unlimited traffic (totalGB=0) are skipped for the corresponding field in the default ADD mode; mode="set" makes a value exactly this, and 0 still leaves a field alone. A client that was auto-disabled solely because it was depleted (expired or over quota) is automatically re-enabled — locally and on its node — when the adjustment lifts it out of depletion; a manually-disabled or still-depleted client is left disabled. The optional flow directive sets the XTLS flow on every client: "none" clears it, "xtls-rprx-vision"/"xtls-rprx-vision-udp443" set it where the inbound supports it (omit or "" to leave it unchanged). Returns the adjusted count and per-email skip reasons. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "addDays": 30,\n  "addBytes": 53687091200,\n  "flow": "xtls-rprx-vision"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "adjusted": 2,\n    "skipped": [\n      { "email": "carol", "reason": "unlimited expiry" }\n    ],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, limitIp: { type: 'integer', nullable: true, minimum: 0, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, comment: { type: 'string', nullable: true, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, tgId: { type: 'integer',
  nullable: true,
  minimum: 0,
  format: 'int64',
  description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, reset: { type: 'integer', nullable: true, minimum: 0, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, totalGB: { type: 'integer', nullable: true, minimum: 0, format: 'int64', description: 'Quota in bytes; 0 is unlimited. Omitted/null leaves unchanged.' }, expiryTime: { type: 'integer', nullable: true, minimum: 0, format: 'int64', description: 'Unix milliseconds; 0 clears expiry. Omitted/null leaves unchanged.' }, flow: { type: 'string', nullable: true, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, group: { type: 'string', nullable: true, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' }, policyId: { type: 'integer', nullable: true, minimum: 0, description: 'Omitted/null leaves unchanged; explicit zero or empty string clears.' } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkSet',
        summary: 'Set fields to exact values on many clients in one call. Only the fields present in the body change; a present zero value clears a field (0 = unlimited totalGB or expiryTime, "" = no flow or group, policyId 0 = no speed plan). limitIp, comment, tgId, reset, flow, totalGB and expiryTime are applied with one read-modify-write per inbound; group and policyId are applied per client. Clients with a delayed-start expiry are skipped for expiryTime. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "limitIp": 3,\n  "comment": "Q4 batch",\n  "expiryTime": 1767225600000,\n  "group": "resellers"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "updated": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [ 'fromInboundIds', 'toInboundIds' ],
  requestProperties: { ...bulkSelectionProperties, fromInboundIds: { type: 'array', items: { type: 'integer' }, minItems: 1 }, toInboundIds: { type: 'array', items: { type: 'integer' }, minItems: 1 } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkMove',
        summary: 'Move many clients between inbounds in one call: each client is attached to every target inbound first and only then detached from the source inbounds, so no client is left without an inbound mid-way. An inbound cannot be both a source and a target. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons, prefixed attach: or detach:.',
        body: '{\n  "emails": ["alice", "bob"],\n  "fromInboundIds": [1],\n  "toInboundIds": [2]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "moved": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkClearIps',
        summary: 'Forget the recorded source IPs of many clients in one call, the same as the per-client clear: their IP-limit history restarts from zero and a client blocked by the limit is unblocked at the next check. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "cleared": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkExport',
        summary: 'Export a selection of clients in the same portable shape GET /clients/export uses for every client, ready for /clients/import on another panel. Instead of emails, pass filter (the same query object /list/paged takes) to export every matching client, capped at 20000.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": [\n    { "client": { "email": "alice", "id": "0c9b6e2a-1f4e-4c2b-9b7e-6c1c1c1c1c1c", "enable": true }, "inboundIds": [1, 2], "externalLinks": [{ "kind": "subscription", "value": "https://mirror.example.com/sub/abc", "remark": "mirror" }] }\n  ]\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkLinks',
        summary: 'Render the share links of many clients in one call, one row per client with every link across its inbounds; a client that could not be rendered carries an error instead. Instead of emails, pass filter (the same query object /list/paged takes) to render every matching client, capped at 20000.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": [\n    { "email": "alice", "links": ["vless://0c9b6e2a-1f4e-4c2b-9b7e-6c1c1c1c1c1c@example.com:443?type=tcp&security=reality#alice"] },\n    { "email": "ghost", "links": [], "error": "client not found" }\n  ]\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, credentials: { type: 'boolean', default: false }, subId: { type: 'boolean', default: false } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkRotate',
        summary: 'Rotate many clients at once: credentials mints a fresh UUID, password, key or secret for every kind the client is attached to (a kind whose credential the panel cannot mint, such as a WireGuard keypair, is skipped with a reason), subId gives the client a new subscription id. Every share link and subscription URL of the selection stops working; handing out the new ones is the operator\'s job. Applied with one read-modify-write per inbound; a local Xray inbound is flagged for restart, a local MTProto sidecar is hot-applied, a node is reconciled. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "credentials": true,\n  "subId": true\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "rotated": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [{ "email": "wg-user", "reason": "no credential to mint for wireguard" }], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkEnable',
        summary: 'Enable many clients in one call. Emails are grouped by inbound and applied with a single read-modify-write per inbound; the running Xray (local or remote node) is updated to add each user. Note that enabling a client whose quota is exhausted or whose expiry has passed only flips the flag — the traffic loop will disable it again on the next tick. Returns the changed count and per-email skip reasons. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "changed": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkDisable',
        summary: 'Disable many clients in one call. Emails are grouped by inbound and applied with a single read-modify-write per inbound; the running Xray (local or remote node) is updated to remove each user. Returns the changed count and per-email skip reasons. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "changed": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, keepTraffic: { type: 'boolean', default: false } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkDel',
        summary: 'Delete many clients in one call. The server processes the list sequentially so each delete sees the committed state of the previous one — avoids the race the per-email fan-out had on the panel side. Pass keepTraffic=true to retain the xray_client_traffic rows after deletion. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "keepTraffic": false\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestProperties: clientCreateRequestProperties,
  requestRequired: [ 'client' ],
  requestSchemaArray: true,
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkCreate',
        summary: 'Create many clients in one call. Body is a JSON array of {client, inboundIds} payloads — the same shape /add accepts. Items are processed sequentially; per-email skip reasons are returned for items that fail (e.g., duplicate email). Triggers a single Xray restart at the end if any inbound was running. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '[\n  {\n    "client": {\n      "email": "alice@example.com",\n      "totalGB": 53687091200,\n      "expiryTime": 0,\n      "enable": true\n    },\n    "inboundIds": [7]\n  },\n  {\n    "client": {\n      "email": "bob@example.com",\n      "totalGB": 53687091200,\n      "expiryTime": 0,\n      "enable": true\n    },\n    "inboundIds": [7, 9]\n  }\n]',
        response: '{\n  "success": true,\n  "obj": {\n    "created": 2,\n    "skipped": [\n      { "email": "alice@example.com", "reason": "email already in use" }\n    ],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [ 'group' ],
  requestProperties: { ...bulkSelectionProperties, group: { type: 'string' } },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/groups/bulkAdd',
        summary: 'Add many clients to a group in one call. Updates clients.group_name and patches the matching client entry inside every owning inbound\'s settings JSON in a single transaction. If the group name does not yet exist (in client_groups or as a derived label), it is auto-created as a persistent group. To clear the group label, use /groups/bulkRemove instead. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "group": "customer-a"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/groups/bulkRemove',
        summary: 'Clear the group label on many clients in one call. Inverse of /groups/bulkAdd. Clients themselves are kept — only the group label is cleared from clients.group_name and from each owning inbound\'s settings JSON. Groups become empty if all their members are removed. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, inboundIds: { type: 'array', items: { type: 'integer' } } },

        method: 'POST',
        path: '/panel/api/clients/bulkAttach',
        summary: 'Attach many existing clients to many inbounds in one call. Each client keeps its identity (email/UUID/password/subId) and a shared traffic row; all clients are added to a target inbound in a single AddInboundClient call. Clients already present on a target are reported under skipped. Returns per-email attached/skipped/errors lists and triggers a single Xray restart if any target inbound was running. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        params: [],
        body: '{\n  "emails": ["alice", "bob"],\n  "inboundIds": [7, 9]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "attached": ["alice", "bob"],\n    "skipped": ["bob"],\n    "errors": [],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties, inboundIds: { type: 'array', items: { type: 'integer' } } },

        method: 'POST',
        path: '/panel/api/clients/bulkDetach',
        summary: 'Mirror of bulkAttach: detach many existing clients from many inbounds in one call. For each email, intersects the client\'s current inbounds with the requested set and detaches from those only; (email, inbound) pairs where the client is not currently attached are silently no-ops. Emails not attached to any of the requested inbounds are reported under skipped. Client records are kept even if they become orphaned — use bulkDel for full removal. Returns per-email detached/skipped/errors lists and triggers a single Xray restart if any target inbound was running. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        params: [],
        body: '{\n  "emails": ["alice", "bob"],\n  "inboundIds": [7, 9]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "detached": ["alice", "bob"],\n    "skipped": [],\n    "errors": [],\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
  requestBodyRequired: true,
  requestRequired: [],
  requestProperties: { ...bulkSelectionProperties,  },
  params: [],

        method: 'POST',
        path: '/panel/api/clients/bulkResetTraffic',
        summary: 'Zero up/down counters for many clients in one call. One batched update over the selection; a client the reset brings back under its quota is re-enabled across its inbounds and pushed to the core, and daemon-side counters (MTProto) are cleared. affected is the number of traffic rows zeroed. Instead of emails, pass filter (the same query object /list/paged takes) to act on every matching client, capped at 20000. The response also carries outcome: per-row ok, skipped and failed lists with reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2,\n    "outcome": { "ok": ["alice", "bob"], "skipped": [], "failed": [] }\n  }\n}',
      },
      {
method: 'GET',
path: '/panel/api/clients/groups',
summary: 'List all client groups with their member counts. Merges persisted groups (rows in client_groups, including empty placeholders) with the distinct group_name values currently set on clients. Sorted alphabetically (case-insensitive).',
responseSchema: 'GroupDetails',
responseSchemaArray: true,
},
      {
        method: 'GET',
        path: '/panel/api/clients/groups/:name/emails',
        summary: 'Return just the email list of clients that currently belong to the given group. Useful for fanning a single bulk action over an entire group without round-tripping the full client list.',
        params: [
          { name: 'name', in: 'path', type: 'string', desc: 'Group name (URL-encoded).' },
        ],
        response: '{\n  "success": true,\n  "obj": ["alice", "bob", "carol"]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/groups/create',
        summary: 'Create a new empty (placeholder) group. The group becomes selectable in client forms and the filter drawer even before any client is added to it. Errors if a group with the same name already exists.',
        body: '{\n  "name": "customer-a"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "name": "customer-a"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/groups/rename',
        summary: 'Rename a group. The new name is applied to the client_groups row AND propagated to every matching client (both clients.group_name and the client entry inside every owning inbound\'s settings JSON) in a single transaction. Returns the number of clients whose label was updated.',
        body: '{\n  "oldName": "customer-a",\n  "newName": "tier-1"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 5\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/groups/delete',
        summary: 'Remove a group. Deletes the client_groups row and clears the group label from every matching client (both clients.group_name and the inbound settings JSON). The clients themselves are NOT deleted — use /bulkDel after filtering by group for that. Returns the count of clients whose label was cleared.',
        body: '{\n  "name": "customer-a"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 5\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/groups/resetTraffic',
        summary: 'Reset only the group-level traffic counter shown on the groups page. Snapshots the current up/down sum of the group\'s members as a baseline so the group total reads zero, while leaving each client\'s own counters (and their quotas) untouched. No Xray restart is triggered. Creates the client_groups row if the group exists only as a derived label.',
        body: '{\n  "name": "customer-a"\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "name": "customer-a"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/resetTraffic/:email',
        summary: 'Zero out a single client’s up/down counters. Re-enables the client across every attached inbound and pushes the change to Xray (or the remote node) so depleted users can connect again immediately.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/clients/updateTraffic/:email',
        summary: 'Manually adjust a client’s upload + download counters. Useful for migrations from external accounting systems.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
        ],
        body: '{\n  "upload": 1073741824,\n  "download": 5368709120\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/ips/:email',
        summary: 'List source IPs that have connected with the given client’s credentials. Returns an array of "ip (timestamp)" strings.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/clients/localAddresses/:email',
        summary:
          'Where this client sits inside each core’s tunnel, which is the address other clients on the same inbound reach it at. Read live from the cores rather than from stored history, so only a live tunnel on a local inbound appears, and a core whose users share the host’s stack — Xray — honestly reports none.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
        ],
        responseSchema: 'ClientLocalAddress',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/clients/clearIps/:email',
        summary: 'Reset the recorded IP list for a client.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/clients/onlines',
        summary: 'List the emails of currently connected clients (last seen within the heartbeat window), deduped across every node.',
        response: '{\n  "success": true,\n  "obj": ["user1", "user2"]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/onlinesByGuid',
        summary: 'Online client emails grouped by the panelGuid of the node that physically hosts each client. The local panel uses its own GUID; each node (at any depth in a chain) uses its GUID. Lets the inbounds page attribute online status to the real node instead of the intermediate one it syncs through.',
        response: '{\n  "success": true,\n  "obj": {\n    "a1b2-...": ["user1"],\n    "c3d4-...": ["user1", "user2"]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/clientIpsByGuid',
        summary: 'Per-client source IPs grouped by the panelGuid of the node that observed them. Lets the central panel attribute and enforce per-client IP limits using the real visitor IPs each node sees, instead of the address of the intermediate panel it syncs through.',
        response: '{\n  "success": true,\n  "obj": {\n    "a1b2-...": {\n      "user1": [\n        { "ip": "1.2.3.4", "timestamp": 1700000000 }\n      ]\n    }\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/activeInbounds',
        summary: 'Inbound tags that carried traffic within the heartbeat window, grouped by the hosting node\'s panelGuid. Pairs with onlinesByGuid so the inbounds page only marks a multi-inbound client online on the inbounds it actually used. Nodes that do not report per-inbound activity are absent.',
        response: '{\n  "success": true,\n  "obj": {\n    "a1b2-...": ["in-443-tcp", "in-8443-tcp"]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/lastOnline',
        summary: 'Map of client email → last-seen unix timestamp.',
        response: '{\n  "success": true,\n  "obj": {\n    "user1": 1700000000,\n    "user2": 1699999000\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/traffic/:email',
        summary: 'Traffic counters for a client identified by email.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique across the panel).' },
        ],
        responseSchema: 'ClientTraffic',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/subLinks/:subId',
        summary:
          'Return every protocol URL (vless://, vmess://, trojan://, ss://, hysteria://, hy2://) for clients matching the subscription ID. Same result set as /sub/<subId>, but as a JSON array — no base64. When an inbound has streamSettings.externalProxy set, one URL is emitted per external proxy. Empty array when the subId matches no client on an enabled inbound. A disabled client is still listed — its credential is stripped from the running core, so the link cannot connect; use the {{STATUS}} remark token to label it.',
        params: [
          { name: 'subId', in: 'path', type: 'string', desc: "Subscription ID, taken from the client's subId field." },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?security=reality&...#user1",\n    "vmess://eyJ2IjoyLC..."\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/configFile/:subId/:inboundId',
        summary:
          'Return the importable config file for one client on one inbound — today the .conf a WireGuard or AmneziaWG client imports, rendered by the core that owns the kind, so it carries that inbound\'s obfuscation parameters. Same bytes the subscription file endpoint serves. Errors when the subscription/inbound pair has nothing file-shaped to hand out.',
        params: [
          { name: 'subId', in: 'path', type: 'string', desc: "Subscription ID, taken from the client's subId field." },
          { name: 'inboundId', in: 'path', type: 'integer', desc: 'Inbound the config is for.' },
        ],
        responseSchema: 'ClientConfigFile',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/links/:email',
        summary:
          "Return every URL for one client across all attached inbounds — the same strings the Copy URL button copies in the panel UI. Supported protocols: vmess, vless, trojan, shadowsocks, hysteria. If streamSettings.externalProxy is set, returns one URL per external proxy. Protocols without a URL form (socks, http, mixed, wireguard, dokodemo, tunnel) contribute nothing.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?...#user1"\n  ]\n}',
      },

{
  method: 'POST',
  path: '/panel/api/clients/groups/quota',
  summary: 'Admin global group pooled quota and optional shared inbound replacement; absent inboundIds preserves set, [] clears managed set.',
  responseSchema: 'GroupQuotaRequest',
  params: [
    {
      name: 'name',
      in: 'body',
      type: 'string'
    },
    {
      name: 'quotaBytes',
      in: 'body',
      type: 'integer'
    },
    {
      name: 'inboundIds',
      in: 'body',
      type: 'integer[]',
      optional: true
    }
  ]
},
],
  },

  {
    id: 'nodes',
    title: 'Nodes',
    description:
      'Manage remote Penhoon UI panels acting as nodes for a central panel. All endpoints under /panel/api/nodes.',
    endpoints: [
      {
method: 'GET',
path: '/panel/api/nodes/list',
summary: 'List every configured node with its connection details, health, and last heartbeat patch.',
responseSchema: 'NodeView',
responseSchemaArray: true,
},
      {
        method: 'POST',
        path: '/panel/api/nodes/mtls/ca',
        summary: "This panel's node-auth CA certificate (public, PEM) to paste into a node's mTLS trust setting. Lazily mints the CA and the master client cert on first call. Pair with setting tlsVerifyMode=mtls on the node.",
        response: '{\n  "success": true,\n  "obj": {\n    "caCert": "-----BEGIN CERTIFICATE-----\\n...\\n-----END CERTIFICATE-----\\n"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/mtls/trustCA',
        summary: "Set the CA certificate this panel trusts for incoming node-API client certificates (this panel acting as a node). Paste the managing panel's CA (from nodes/mtls/ca). An empty caCert disables it. A non-empty value must be a PEM certificate. Applied on the next panel restart.",
        body: '{\n  "caCert": "-----BEGIN CERTIFICATE-----\\n...\\n-----END CERTIFICATE-----\\n"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/get/:id',
        summary: 'Fetch a single node by ID.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
        responseSchema: 'NodeView',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/webCert/:id',
        summary: 'Fetch a node\'s own web TLS certificate/key file paths (proxied to the node). Used by the inbound form\'s "Set Cert from Panel" so a node-assigned inbound gets paths that exist on the node, not the central panel.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
        response: '{\n  "success": true,\n  "obj": {\n    "webCertFile": "/root/cert/example.com/fullchain.pem",\n    "webKeyFile": "/root/cert/example.com/privkey.pem"\n  }\n}',
      },
      {
  requestSchema: 'NodeMutationRequest',
  requestRequired: [ 'name', 'address', 'port' ],

        method: 'POST',
        path: '/panel/api/nodes/add',
        summary: 'Register a new remote node. Provide its URL, write-only apiToken, and optional remark / allowPrivateAddress flag. Responses expose hasApiToken only.',
        body:
          '{\n  "name": "de-fra-1",\n  "remark": "",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef...",\n  "clearApiToken": false,\n  "enable": true,\n  "allowPrivateAddress": false\n}',
        responseSchema: 'NodeView',
      },
      {
  requestSchema: 'NodeMutationRequest',
  requestRequired: [ 'name', 'address', 'port' ],

        method: 'POST',
        path: '/panel/api/nodes/update/:id',
        summary: 'Replace a node\u2019s connection details. apiToken is write-only: omit it or send an empty string to keep the stored token; set clearApiToken=true to clear it.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
        body: '{\n  "name": "de-fra-1",\n  "remark": "",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "",\n  "clearApiToken": false,\n  "enable": true,\n  "allowPrivateAddress": false\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/del/:id',
        summary: 'Delete a node. Inbounds bound to it are not auto-migrated.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/setEnable/:id',
        summary: 'Pause or resume traffic sync with this node.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
        body: '{\n  "enable": true\n}',
      },
      {
  requestSchema: 'NodeMutationRequest',
  requestRequired: [ 'name', 'address', 'port' ],

        method: 'POST',
        path: '/panel/api/nodes/test',
        summary: 'Probe a node without saving it. Uses the body as connection details and returns the same heartbeat snapshot a registered node would have.',
        body: '{\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef...",\n  "name": "de-fra-1"\n}',
        responseSchema: 'ProbeResultUI',
      },
      {
  requestSchema: 'NodeMutationRequest',
  requestRequired: [ 'name', 'address', 'port' ],

        method: 'POST',
        path: '/panel/api/nodes/certFingerprint',
        summary: "Connect to the node over HTTPS without verifying its certificate and return the leaf certificate's SHA-256 (base64). Used by the Add/Edit Node dialog to fetch and pin a self-signed certificate. Uses the same body as /test.",
        body: '{\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "name": "de-fra-1"\n}',
        response: '{\n  "success": true,\n  "obj": "k3b1...base64-sha256...="\n}',
      },
      {
  requestSchema: 'NodeMutationRequest',
  requestRequired: [ 'name', 'address', 'port' ],

        method: 'POST',
        path: '/panel/api/nodes/inbounds',
        summary: 'Use unsaved node connection details to list the remote inbounds available for selective import.',
        body: '{\n  "name": "de-fra-1",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef..."\n}',
        response: '{\n  "success": true,\n  "obj": [\n    { "tag": "inbound-443", "remark": "VLESS", "protocol": "vless", "port": 443 }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/probe/:id',
        summary: 'Probe an existing node, updating its cached health state.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/updatePanel',
        summary: 'Trigger the official panel self-updater on each given node (downloads the latest release and restarts). Only enabled, online nodes are updated; offline/disabled ones are reported as skipped. Set "dev": true to move the nodes to the rolling per-commit dev channel instead of the latest stable release. Returns a per-node result list.',
        body: '{\n  "ids": [1, 2, 3],\n  "dev": false\n}',
        response: '{\n  "success": true,\n  "obj": [\n    { "id": 1, "name": "de-1", "ok": true },\n    { "id": 2, "name": "fr-1", "ok": false, "error": "node is offline" }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/history/:id/:metric/:bucket',
        summary: 'Aggregated metric history for a node — same shape as /server/history, scoped to one node.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
          { name: 'metric', in: 'path', type: 'string', desc: 'cpu | mem.' },
          { name: 'bucket', in: 'path', type: 'number', desc: 'Bucket size in seconds. Allowed: 2, 15, 30, 60, 180, 360, 720, 1440, 2880, 10080.' },
        ],
      },
    ],
  },

  {
    id: 'routing',
    title: 'Routing',
    description:
      "Operator routing intent: which inbounds, which criteria, which destination. A rule names inbounds by ID rather than by tag, so renaming or deleting an inbound cannot silently widen it to every inbound on the box. The compile decides which mechanism realises each rule -- an Xray inbound is named directly, while a kernel inbound is fronted and its rule rewritten onto the front's tag -- which is what lets one screen route any protocol to any protocol. A rule naming an inbound the router cannot see is refused at save rather than accepted and left to never match. All endpoints under /panel/api/routing.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/routing/rules',
        summary: "List every routing rule in the operator's own first-match order. Each rule carries a diags array when the LAST config generation could not realise it -- one entry per (rule, subject) pair, with a messageKey rather than a sentence so the panel translates it. Saving an unrealisable rule is already refused, so these are the rules that went inert afterwards: the inbound they name was disabled, their exit stopped resolving, or the Xray template changed under them. The key is omitted entirely for a rule that is being applied, and on a panel that has not generated a config since it started.",
        responseSchema: 'RuleView',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/routing/subjects',
        summary:
          "Which inbounds a rule may name. An inbound that cannot be a subject right now is listed with routable=false and a blockedKey naming why, never omitted. criteriaMask names the criteria that can actually match on it -- user is absent for a kernel ingress because Xray's tun handler builds a user with no email, so the matcher can never return true.",
        responseSchema: 'RoutingSubjectView',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/routing/exits',
        summary:
          "Which uplinks a rule may be pointed at. Operator-owned exits only -- a panel-owned front is machinery for an ingress and never a destination. The handle that realises an exit is deliberately absent: which device or port carries it is the compile's business, and an editor that showed it would invite an operator to depend on it.",
        responseSchema: 'RoutingExitView',
        responseSchemaArray: true,
      },
      {
  requestSchema: 'RoutingRule',
  requestRequired: [ 'destKind' ],
  requestProperties: { ingressIds: { type: 'string', description: 'JSON-encoded inbound ID array; used with selected scope.' },
  criteria: { type: 'string', description: 'JSON-encoded criteria object.' } },

        method: 'POST',
        path: '/panel/api/routing/rules',
        summary:
          'Create a rule, append it last, and converge. A convergence failure returns success: false with the persisted rule in obj; retry by updating obj.id instead of creating again. A validation refusal returns no persisted rule.',
        responseSchema: 'RoutingRule',
      },
      {
  requestBodyRequired: true,
  params: [ { name: 'ids', in: 'body', type: 'integer[]', optional: true, desc: 'Whole rule ID list in desired order.' } ],

        method: 'POST',
        path: '/panel/api/routing/rules/order',
        summary:
          'Set the whole first-match order in one write. The body is the complete id list; a partial application would silently change which rule wins.',
      },
      {
  requestSchema: 'RoutingRule',
  requestRequired: [ 'destKind' ],
  requestProperties: { ingressIds: { type: 'string', description: 'JSON-encoded inbound ID array; used with selected scope.' },
  criteria: { type: 'string', description: 'JSON-encoded criteria object.' } },

        method: 'POST',
        path: '/panel/api/routing/rules/:id',
        summary:
          'Update one rule and converge. A convergence failure returns success: false with the persisted rule in obj; retry the same rule ID. A validation refusal leaves the stored rule unchanged and returns no persisted rule.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Routing rule ID.' }],
        responseSchema: 'RoutingRule',
      },
      {
        method: 'POST',
        path: '/panel/api/routing/rules/:id/del',
        summary: 'Delete one rule and converge.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Routing rule ID.' }],
      },
    ],
  },

  {
    id: 'egresses',
    title: 'Egresses',
    description:
      "A policy-routed exit an L3 inbound's traffic leaves through. The panel owns one ip rule per attached inbound, a private routing table and its blackhole; the front device belongs to the core the egress type names. Everything the kernel needs is derived from the egress id, so an id is never reused. ICMP is answered by the front itself, so a ping or traceroute through an egress succeeds for any destination whatsoever, including one nothing could reach — test an egress with a TCP or UDP request instead. Master-local: an inbound assigned to a node cannot be attached yet. All endpoints under /panel/api/egresses.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/egresses/list',
        summary: 'List every egress in id order.',
        responseSchema: 'Egress',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/egresses/get/:id',
        summary: 'Fetch a single egress by ID.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Egress ID.' }],
        responseSchema: 'Egress',
      },
      {
        method: 'GET',
        path: '/panel/api/egresses/preflight',
        summary:
          "What stops this host carrying an egress: a foreign rule in the reserved 31001-31999 priority band, a foreign route in tables 30001-30999, a gateway prefix already on the box, net.ipv4.conf.all.rp_filter=1 (the effective value is max(all, dev), so the panel cannot lower it per device), or no permission to write a routing rule at all. Notes report host facts the panel does not own: either forwarding knob being off, nft not being installed (so traffic forwarded out of an L3 inbound keeps its in-tunnel source and is dropped upstream), and any enabled egress whose front device is not on this host, which leaves everything attached to it contained rather than routed. forwardingNotes repeats the forwarding half and the nft one, because a kernel WireGuard inbound needs both whether or not any egress exists — without them that inbound completes handshakes and carries nothing.",
        response:
          '{\n  "success": true,\n  "obj": {\n    "ok": true,\n    "refusals": [],\n    "notes": ["net.ipv6.conf.all.forwarding is 0, so no L3 inbound on this host forwards a packet of that family at all, egress or not"],\n    "forwardingNotes": ["net.ipv6.conf.all.forwarding is 0, so no L3 inbound on this host forwards a packet of that family at all, egress or not"]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/egresses/add',
        summary:
          'Create an egress. type must be a driver this build serves (xray-tun) and target must already name an outbound or a balancer — an unresolvable target is refused rather than stored, because a stored one would leave the egress dark.',
        body: '{\n  "type": "xray-tun",\n  "remark": "warp exit",\n  "target": "warp",\n  "enable": true,\n  "settings": ""\n}',
        responseSchema: 'Egress',
      },
      {
        method: 'POST',
        path: '/panel/api/egresses/update/:id',
        summary:
          'Replace the editable half of an egress. The id, and so the routing table, rule priority, front device and gateway derived from it, are fixed for the row’s life. Disabling a row that inbounds still select is refused: it would move them to direct.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Egress ID.' }],
        body: '{\n  "type": "xray-tun",\n  "remark": "warp exit",\n  "target": "warp",\n  "enable": true,\n  "settings": ""\n}',
        responseSchema: 'Egress',
      },
      {
        method: 'POST',
        path: '/panel/api/egresses/test',
        summary:
          'Time a real request out through each egress and report the exit it came from, so an uplink answers the same question an outbound does. mode is "tcp", "http" or "real"; "tcp" is promoted to the HTTP probe because a bare dial proves nothing about a tunnel whose peer ignores unauthenticated packets. Every probe is gated on the host agreeing that this egress\'s mark reaches its own device: a mark no rule catches falls through to the main table and leaves with the server\'s own address, so a disabled row, a contained one, or one whose device is gone answers with why rather than with a latency that measured the direct path. A front is refused outright — it hands its traffic to an outbound tag, and that outbound is testable on its own row.',
        body: '{\n  "ids": [18],\n  "mode": "real"\n}',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "tag": "US-sfo | Surfshark (kernel)",\n      "mode": "real",\n      "success": true,\n      "delay": 142,\n      "httpStatus": 204,\n      "egress": { "ipv4": "169.150.196.10", "country": "US" }\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/egresses/hostkey',
        summary:
          "Perform the key exchange with an SSH server and report the host key it presents, without authenticating, so an SSH exit's fingerprint field can be filled with what the server really answers and the operator only has to confirm the pin. Nothing is stored: the exit is saved with whatever fingerprint the operator submits.",
        body: '{\n  "host": "91.107.151.123",\n  "port": 12878\n}',
        responseSchema: 'SSHHostKey',
      },
      {
        method: 'POST',
        path: '/panel/api/egresses/del/:id',
        summary:
          'Delete an egress and bring down the kernel state its id owns. Refused while a routing rule still sends traffic to it.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Egress ID.' }],
      },
    ],
  },

  {
    id: 'policies',
    title: 'Policies',
    description:
      'Reusable client plans combine traffic quotas, duration, device limits, inbound assignments and speed tiers. Template changes are tracked separately from applied client settings; reapply updates members while preserving explicit overrides unless requested otherwise. Quota continuation, renewal and rate shaping are validated against each core capability. Zero bits per second means unlimited in that direction. Administrator plans use /panel/api/policies; permitted resellers manage their own plans under /panel/api/reseller/plans.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/policies/list',
        summary: 'List every plan in id order.',
        responseSchema: 'Policy',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/policies/get/:id',
        summary: 'Fetch a single plan by ID.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Policy ID.' }],
        responseSchema: 'Policy',
      },
      {
        method: 'GET',
        path: '/panel/api/policies/enforced/:email',
        summary:
          'What one client’s rules currently work out to, beside what the kernel is actually holding for them. The enforced figures are read back out of the kernel rather than echoed from what the panel pushed, so a limit that never landed is visible instead of merely claimed. shapeable says whether the kernel is holding state for this client at this moment, which needs both a core that can identify them and a plan granting them a rate — so a client on no plan reads false, and the enforced figures mean nothing until it is true. unresolved means the client is assigned a plan that no longer exists, which never throttles.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'EnforcedLimits',
      },
      {
  requestSchema: 'Policy',
  requestRequired: [ 'name' ],
  requestProperties: { tiers:
   { type: 'array',
     items:
      { type: 'object',
        properties:
         { fromBytes: { type: 'integer', format: 'int64' }, upBps: { type: 'integer', format: 'int64' }, downBps: { type: 'integer', format: 'int64' } } } } },

method: 'POST',
path: '/panel/api/policies/add',
summary:
          'Create a plan. tiers is sorted by fromBytes and de-duplicated on write, so the stored ladder is canonical; two tiers starting at the same usage keep the first. upBps and downBps are bits per second from the client’s point of view.',
body: '{\n  "name": "fair use",\n  "tiers": [\n    { "fromBytes": 0, "upBps": 0, "downBps": 0 },\n    { "fromBytes": 53687091200, "upBps": 10000000, "downBps": 10000000 },\n    { "fromBytes": 107374182400, "upBps": 2000000, "downBps": 2000000 }\n  ]\n}',
responseSchema: 'Policy',
params: [
  {
    name: 'description',
    in: 'body',
    type: 'string',
    optional: true
  },
  {
    name: 'quotaBytes',
    in: 'body',
    type: 'integer',
    optional: true,
    desc: 'Null is unmanaged; zero is unlimited.'
  },
  {
    name: 'durationDays',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'limitIp',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'inboundIds',
    in: 'body',
    type: 'integer[]',
    optional: true
  },
  {
    name: 'startOnFirstUse',
    in: 'body',
    type: 'boolean',
    optional: true
  },
  {
    name: 'autoRenewDays',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'resetCycle',
    in: 'body',
    type: 'string',
    optional: true,
    desc: 'none, weekly or monthly'
  },
  {
    name: 'onDepletion',
    in: 'body',
    type: 'string',
    optional: true,
    desc: 'disable, throttle or notify; runtime capability restrictions are enforced.'
  }
],
},
      {
  requestSchema: 'Policy',
  requestRequired: [],
  requestProperties: { tiers:
   { type: 'array',
     items:
      { type: 'object',
        properties:
         { fromBytes: { type: 'integer', format: 'int64' }, upBps: { type: 'integer', format: 'int64' }, downBps: { type: 'integer', format: 'int64' } } } } },

method: 'POST',
path: '/panel/api/policies/update/:id',
summary:
          'Replace a plan’s name and ladder. Every client on it moves to their new tier on the next pass, which costs one kernel class change per direction and drops nothing.',
params: [
  {
    name: 'id',
    in: 'path',
    type: 'number',
    desc: 'Policy ID.'
  },
  {
    name: 'description',
    in: 'body',
    type: 'string',
    optional: true
  },
  {
    name: 'quotaBytes',
    in: 'body',
    type: 'integer',
    optional: true,
    desc: 'Null is unmanaged; zero is unlimited.'
  },
  {
    name: 'durationDays',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'limitIp',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'inboundIds',
    in: 'body',
    type: 'integer[]',
    optional: true
  },
  {
    name: 'startOnFirstUse',
    in: 'body',
    type: 'boolean',
    optional: true
  },
  {
    name: 'autoRenewDays',
    in: 'body',
    type: 'integer',
    optional: true
  },
  {
    name: 'resetCycle',
    in: 'body',
    type: 'string',
    optional: true,
    desc: 'none, weekly or monthly'
  },
  {
    name: 'onDepletion',
    in: 'body',
    type: 'string',
    optional: true,
    desc: 'disable, throttle or notify; runtime capability restrictions are enforced.'
  }
],
body: '{\n  "name": "fair use",\n  "tiers": [\n    { "fromBytes": 0, "upBps": 0, "downBps": 0 },\n    { "fromBytes": 53687091200, "upBps": 10000000, "downBps": 10000000 }\n  ]\n}',
responseSchema: 'Policy',
},
      {
        method: 'POST',
        path: '/panel/api/policies/del/:id',
        summary:
          'Delete a plan. Every client assigned to it keeps their assignment row with no plan attached, so the operator can see who lost one; those clients are unlimited until they are reassigned.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Policy ID.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/policies/assign',
        summary:
          'Put one client on a plan, or take them off it with policyId 0. Keyed by email, like the client’s quota, so the assignment survives a delete-and-resync that mints the client a new id.',
        body: '{\n  "email": "user1",\n  "policyId": 1\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/policies/pushAssignments',
        summary: 'Receive a master panel\'s resolved speed ladder for each client this node serves, keyed by the master\'s GUID. Stored beside the client and evaluated by the node\'s own policy pass from the usage the same master pushes; a client of that master absent from the push loses its ladder. Called panel-to-panel by the node traffic sync job.',
        params: [
          { name: 'masterGuid', in: 'body (json)', type: 'string', desc: 'Stable GUID of the pushing master panel.' },
          { name: 'assignments', in: 'body (json)', type: 'object[]', desc: 'One entry per client on a plan with a ladder: email and tiers.' },
        ],
        body: '{\n  "masterGuid": "9f6c2d-…",\n  "assignments": [\n    { "email": "alice", "tiers": [ { "fromBytes": 0, "upBps": 10000000, "downBps": 10000000 } ] }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/policies/pushBans',
        summary: 'Receive every device-cap ban a master panel holds for this node, the whole set each push, keyed by the master\'s GUID. The node then holds exactly that set from that master and drops any it no longer sends; bans this node decided itself are never touched, and nothing is held while banning is off here. Node-only, over mTLS.',
        params: [
          { name: 'masterGuid', in: 'body (json)', type: 'string', desc: 'Stable GUID of the pushing master panel.' },
          { name: 'bans', in: 'body (json)', type: 'object[]', desc: 'One entry per banned (client, source): clientEmail, source, ports (CSV of this node\'s inbound ports), reason, until (unix seconds).' },
        ],
        body: '{\n  "masterGuid": "9f6c2d-…",\n  "bans": [\n    { "clientEmail": "alice", "source": "203.0.113.7", "ports": "443,8443", "reason": "over-cap", "until": 1788700000 }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/policies/bans',
        summary: 'List every device-cap ban this panel decided: held on this host, or held for one of its nodes (node names the node). A row whose reason is "shared-skipped" is a report, not a ban: the source is shared with another client under its cap and the operator chose not to drop it. Rows lapse at until.',
        responseSchema: 'BanView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/policies/bans/release',
        summary: 'Release one (client, source) wherever this panel banned it: on this host at once, on a node with the next sync push. Answers how many rows were released.',
        params: [ { name: 'email', in: 'body (json)', type: 'string', desc: 'The client the ban was recorded for; empty for an operator ban.', optional: true },
  { name: 'source', in: 'body (json)', type: 'string', desc: 'The banned source address.' } ],
        body: '{\n  "email": "alice",\n  "source": "203.0.113.7"\n}',
        response: '{\n  "success": true,\n  "obj": { "released": 1 }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/policies/bans/releaseAll',
        summary: 'Release every ban this panel decided, now.',
        response: '{\n  "success": true,\n  "obj": { "released": 3 }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/policies/bans/add',
        summary: 'Ban an address by hand: dropped on every local inbound port, resolved each pass so an inbound added later is inside it, for the given minutes or the operator\'s default ban duration.',
        params: [ { name: 'source', in: 'body (json)', type: 'string', desc: 'The address to drop.' },
  { name: 'minutes', in: 'body (json)', type: 'number', desc: 'How long; 0 or absent uses the ban duration setting.', optional: true } ],
        body: '{\n  "source": "203.0.113.7",\n  "minutes": 60\n}',
        responseSchema: 'ClientBan',
      },

{
  method: 'GET',
  path: '/panel/api/policies/summary',
  summary: "Plan summary within the authenticated owner's scope.",
  params: [],
  responseSchema: 'PlanSummary',
  responseSchemaArray: false
},
{
  method: 'GET',
  path: '/panel/api/policies/members/:id',
  summary: "Plan members within the authenticated owner's scope.",
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'integer'
    },
    {
      name: 'page',
      in: 'query',
      type: 'integer',
      optional: true,
      defaultValue: 1
    },
    {
      name: 'pageSize',
      in: 'query',
      type: 'integer',
      optional: true,
      defaultValue: 20
    }
  ],
  responseSchema: 'PlanMemberPage',
  responseSchemaArray: false
},
{
  method: 'POST',
  path: '/panel/api/policies/archive/:id',
  summary: "Plan archive within the authenticated owner's scope.",
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'integer'
    },
    {
      name: 'archived',
      in: 'body',
      type: 'boolean'
    }
  ],
  responseSchema: 'Policy',
  responseSchemaArray: false
},
{
  method: 'POST',
  path: '/panel/api/policies/duplicate/:id',
  summary: "Plan duplicate within the authenticated owner's scope.",
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'integer'
    },
    {
      name: 'name',
      in: 'body',
      type: 'string'
    }
  ],
  responseSchema: 'Policy',
  responseSchemaArray: false
},
{
  method: 'POST',
  path: '/panel/api/policies/apply',
  summary: "Plan apply within the authenticated owner's scope.",
  params: [
    {
      name: 'email',
      in: 'body',
      type: 'string'
    },
    {
      name: 'policyId',
      in: 'body',
      type: 'integer'
    },
    {
      name: 'clearOverrides',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false
    }
  ],
  responseSchema: 'PlanApplyResult',
  responseSchemaArray: false
},
{
  requestBodyRequired: true,

  method: 'POST',
  path: '/panel/api/policies/reapply/:id',
  summary: "Plan reapply within the authenticated owner's scope.",
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'integer'
    },
    {
      name: 'clearOverrides',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false
    }
  ],
  responseSchema: 'PlanReapplyResult',
  responseSchemaArray: false
},
],
  },

  {
    id: 'hosts',
    title: 'Hosts',
    description:
      'Per-inbound override endpoints. Each enabled host renders one extra subscription link/proxy with its own address/port/TLS, superseding the legacy externalProxy array. All endpoints under /panel/api/hosts.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/hosts/list',
        summary: 'List every host across all inbounds, grouped by inbound then ordered by sort order.',
        responseSchema: 'HostGroup',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/hosts/get/:groupId',
        summary: 'Fetch a single host group by Group ID.',
        params: [
          { name: 'groupId', in: 'path', type: 'string', desc: 'Host Group ID.' },
        ],
        responseSchema: 'HostGroup',
      },
      {
        method: 'GET',
        path: '/panel/api/hosts/byInbound/:inboundId',
        summary: "Fetch one inbound's hosts, grouped by host group.",
        params: [
          { name: 'inboundId', in: 'path', type: 'number', desc: 'Inbound ID.' },
        ],
        responseSchema: 'HostGroup',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/hosts/tags',
        summary: 'Distinct, sorted set of tags used across all hosts.',
        response: '{\n  "success": true,\n  "obj": ["CDN", "EU", "FAST"]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/add',
        summary: 'Create a host group on inbounds. A security of reality in front of an inbound that serves tls or none, or tls/none in front of a REALITY inbound, is refused naming the inbound; same, and a tls or none front for a tls or plain inbound, are accepted.',
        body: '{\n  "inboundIds": [1],\n  "remark": "cdn-front",\n  "hosts": ["cdn.example.com"],\n  "port": 8443,\n  "security": "same",\n  "tags": ["CDN"]\n}',
        responseSchema: 'Host',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/update/:groupId',
        summary: 'Replace a host group’s content. A security of reality in front of an inbound that serves tls or none, or tls/none in front of a REALITY inbound, is refused naming the inbound; same, and a tls or none front for a tls or plain inbound, are accepted.',
        params: [
          { name: 'groupId', in: 'path', type: 'string', desc: 'Host Group ID.' },
        ],
        body: '{\n  "inboundIds": [1],\n  "remark": "cdn-front",\n  "hosts": ["cdn.example.com"],\n  "port": 8443,\n  "security": "same",\n  "tags": ["CDN"]\n}',
        responseSchema: 'Host',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/del/:groupId',
        summary: 'Delete a host group.',
        params: [
          { name: 'groupId', in: 'path', type: 'string', desc: 'Host Group ID.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/setEnable/:groupId',
        summary: 'Enable or disable a host group.',
        params: [
          { name: 'groupId', in: 'path', type: 'string', desc: 'Host Group ID.' },
        ],
        body: '{\n  "enable": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/reorder',
        summary: 'Set host group sort order by the position of each groupId in the array.',
        body: '{\n  "ids": ["abc-123", "def-456"]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/bulk/add',
        summary: 'Add a host group to inbounds (same as /add).',
        body: '{\n  "inboundIds": [1, 2],\n  "hosts": ["cdn.example.com", "cdn2.example.com:443"],\n  "remark": "Cloudflare CDN",\n  "port": 0,\n  "security": "same",\n  "isDisabled": false\n}',
        responseSchema: 'Host',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/bulk/setEnable',
        summary: 'Enable or disable many host groups in one call.',
        body: '{\n  "ids": ["abc-123", "def-456"],\n  "enable": false\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/hosts/bulk/del',
        summary: 'Delete many host groups in one call.',
        body: '{\n  "ids": ["abc-123", "def-456"]\n}',
      },
    ],
  },

  {
    id: 'backup',
    title: 'Backup',
    description: 'PostgreSQL snapshots, retention, scheduled backups, authenticated encryption for manual archives, restore operations and optional delivery to configured administrator Telegram chats. Backups and protected configuration require administrator write access.',
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/backuptotgbot',
        summary: 'Send a fresh DB backup to every Telegram chat configured as an admin recipient. No body, no params.',
      },

{
  method: 'GET',
  path: '/panel/api/backups',
  summary: 'List persisted backup and restore operations, archive availability and the UTC schedule.',
  responseSchema: 'BackupStatusView',
  params: []
},
{
  method: 'POST',
  path: '/panel/api/backups/schedule',
  summary: 'Persist automatic backup settings.',
  params: [
    {
      name: 'enabled',
      in: 'body',
      type: 'boolean'
    },
    {
      name: 'cadence',
      in: 'body',
      type: 'string',
      desc: 'hourly, daily (03:00 UTC), weekly (Sunday 03:00 UTC)'
    },
    {
      name: 'keep',
      in: 'body',
      type: 'integer',
      desc: '1–365 snapshots'
    },
    {
      name: 'encrypt',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false,
      desc: 'Must be false. Unattended encryption is rejected because no external key provider is configured; no passphrase is persisted.'
    },
    {
      name: 'sendToTelegram',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false,
      desc: 'Automatic snapshots are unencrypted. Send them only to configured administrator chats.'
    }
  ]
},
{
  method: 'POST',
  path: '/panel/api/backups/create',
  summary: 'Create a complete PostgreSQL archive, optionally encrypted with AES-256-GCM using a request-only passphrase.',
  responseSchema: 'BackupRecordView',
  params: [
    {
      name: 'encrypt',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false
    },
    {
      name: 'passphrase',
      in: 'body',
      type: 'string',
      optional: true,
      desc: 'Required only when encrypt=true; 12–1024 bytes. Never persisted. No passphrase is accepted for an unencrypted archive.'
    },
    {
      name: 'sendToTelegram',
      in: 'body',
      type: 'boolean',
      optional: true,
      defaultValue: false,
      desc: 'Send the exact archive to configured administrator chats; delivery failure preserves the local snapshot.'
    }
  ]
},
{
  method: 'GET',
  path: '/panel/api/backups/:id/download',
  summary: 'Download stored bytes unchanged: .dump.pui ciphertext when encrypted=true, or a plain .dump. No passphrase appears in a URL or download request.',
  responseContent: { 'application/octet-stream': { schema: { type: 'string', format: 'binary' } } },
  responseErrorEnvelope: true,
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'string'
    }
  ]
},
{
  method: 'POST',
  path: '/panel/api/backups/:id/restore',
  summary: 'Restore a stored PostgreSQL archive after making a safety dump.',
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'string'
    },
    {
      name: 'confirm',
      in: 'body (form)',
      type: 'string',
      desc: 'Must be RESTORE'
    },
    {
      name: 'passphrase',
      in: 'body (form)',
      type: 'string',
      optional: true,
      desc: 'Original request-only archive passphrase for encrypted snapshots. Leave blank for plain snapshots.'
    }
  ],
  responseSchema: 'BackupRecordView'
},
{
  method: 'POST',
  path: '/panel/api/backups/upload',
  summary: 'Restore .dump or encrypted .dump.pui archive. Authenticate the entire encrypted stream before any PostgreSQL action.',
  params: [
    {
      name: 'file',
      in: 'body (multipart)',
      type: 'file'
    },
    {
      name: 'confirm',
      in: 'body (multipart)',
      type: 'string',
      desc: 'Must be RESTORE'
    },
    {
      name: 'passphrase',
      in: 'body (multipart)',
      type: 'string',
      optional: true,
      desc: 'Original passphrase, never persisted or logged. Leave blank for a plain PostgreSQL custom-format dump.'
    }
  ],
  responseSchema: 'BackupRecordView'
},
{
  method: 'POST',
  path: '/panel/api/backups/:id/telegram',
  summary: "Send this exact stored archive to the bot's configured administrator chats and persist delivery outcome. Administrator write access required.",
  params: [
    {
      name: 'id',
      in: 'path',
      type: 'string'
    }
  ],
  responseSchema: 'BackupRecordView'
},
],
  },

  {
    id: 'settings',
    title: 'Settings',
    description:
      'Panel configuration and user credentials. All endpoints live under /panel/api/setting and require a logged-in session or Bearer token.',
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/setting/all',
        summary: 'Return every panel setting: web server, Telegram bot, subscription, security, LDAP. The full JSON blob that the Settings page edits.',
        response: '{\n  "success": true,\n  "obj": {\n    "webPort": 2053,\n    "webCertFile": "",\n    "webKeyFile": "",\n    "webBasePath": "/",\n    "subPort": 10882,\n    "subPath": "/sub/",\n    "subClashAutoDetect": false,\n    "subClashUserAgentRegex": "",\n    "subJsonEnable": false,\n    "subJsonAutoDetect": false,\n    "subJsonAlwaysArray": false,\n    "subJsonUserAgentRegex": "",\n    "subJsonPath": "/json/",\n    "subJsonURI": "https://sub.example.com/json/",\n    "subClashEnable": true,\n    "subClashPath": "/clash/",\n    "subClashURI": "https://sub.example.com/clash/",\n    "tgBotEnable": false,\n    "tgBotToken": "",\n    ...\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/defaultSettings',
        summary: 'Return the computed default settings based on the request host. Useful to preview what a fresh install would use.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/factoryDefaults',
        summary: 'Return the shipped (factory) default value per browser-safe setting key, so clients can tell a stored value apart from the default it would fall back to. Per-install material (secret, panelGuid, mTLS keys) and credential fields are never included.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/update',
        summary: 'Persist every setting at once. The body mirrors the shape returned by /all. Invalid values (bad ports, missing cert pairs, etc.) are rejected before write.',
        body: '{\n  "webPort": 2053,\n  "webBasePath": "/",\n  "subPort": 10882,\n  "subPath": "/sub/",\n  "subClashAutoDetect": false,\n  "subClashUserAgentRegex": "",\n  "subJsonEnable": false,\n  "subJsonAutoDetect": false,\n  "subJsonAlwaysArray": false,\n  "subJsonUserAgentRegex": "",\n  "subJsonPath": "/json/",\n  "subJsonURI": "https://sub.example.com/json/",\n  "subClashEnable": true,\n  "subClashPath": "/clash/",\n  "subClashURI": "https://sub.example.com/clash/",\n  "tgBotEnable": false,\n  ...\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/validateRegex',
        summary: 'Validate any regular expression with the backend Go RE2 compiler without saving it.',
        body: '{\n  "regex": "(?m)^general-purpose$"\n}',
        response: '{\n  "success": true,\n  "msg": ""\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/updateUser',
        summary: 'Change the panel admin username and password. Requires the current credentials for verification. The session is refreshed with the new values on success.',
        params: [
          { name: 'oldUsername', in: 'body', type: 'string', desc: 'Current admin username.' },
          { name: 'oldPassword', in: 'body', type: 'string', desc: 'Current admin password.' },
          { name: 'newUsername', in: 'body', type: 'string', desc: 'Desired new username.' },
          { name: 'newPassword', in: 'body', type: 'string', desc: 'Desired new password.' },
        ],
        body: '{\n  "oldUsername": "admin",\n  "oldPassword": "admin",\n  "newUsername": "newadmin",\n  "newPassword": "newpass"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/restartPanel',
        summary: 'Restart the entire p-ui process after a 3-second grace period. The connection drops immediately; the panel comes back online ~5-10 seconds later.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/testSmtp',
        summary: 'Test SMTP connection with stage-by-stage reporting (connect, auth, send). Returns structured result with stage and message.',
        response: '{\n  "success": true,\n  "stage": "send",\n  "msg": "Test email sent successfully"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/testTgBot',
        summary: 'Test Telegram bot connection by sending a test message to the configured chat.',
        response: '{\n  "success": true,\n  "msg": "Test message sent to Telegram"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/setting/getDefaultJsonConfig',
        summary: 'Return the built-in default Xray JSON config template that ships with this panel version.',
      },
    ],
  },

  {
    id: 'api-tokens',
    title: 'API Tokens',
    description:
      'Manage administrator Bearer tokens for automation. Tokens have a name, enabled flag, scope, expiry and optional source-IP/CIDR restrictions. Disabling or deleting a token revokes it. Tokens are stored as SHA-256 hashes; plaintext is returned only at creation. Send one as <code>Authorization: Bearer &lt;token&gt;</code>. Read-only tokens cannot change state or export protected configuration. Reseller tokens are managed separately and retain their account ownership and permissions.',
    endpoints: [
      {
method: 'GET',
path: '/panel/api/setting/apiTokens',
summary: 'List every API token, enabled or not. The token value is never returned — only metadata.',
responseSchema: 'SettingsAPITokenView',
responseSchemaArray: true,
},
      {
method: 'POST',
path: '/panel/api/setting/apiTokens/create',
summary: 'Mint a new API token. Name must be unique and 1-64 characters; the token string is server-generated and returned only in this response — it is stored hashed and cannot be retrieved later.',
params: [ { name: 'name', in: 'body', type: 'string', desc: 'Human-readable label, e.g. "central-panel-a".' },
  { name: 'scope',
    in: 'body',
    type: 'string',
    desc: '"admin" (the default) or "read": a read token may call GET endpoints and nothing else.',
    optional: true },
  { name: 'expiresAt',
    in: 'body',
    type: 'number',
    desc: 'Unix time in seconds after which the token stops authenticating; 0 or absent = never.',
    optional: true },
  { name: 'allowedIPs',
    in: 'body',
    type: 'string',
    optional: true,
    desc: 'Comma-separated source IP/CIDR allowlist, maximum 64 networks. Empty permits any source.' } ],
body: '{\n  "name": "central-panel-a",\n  "scope": "read",\n  "expiresAt": 1798761600\n}',
responseSchema: 'SettingsAPITokenView',
errorResponse: '{\n  "success": false,\n  "msg": "a token with that name already exists"\n}',
},
      {
        method: 'POST',
        path: '/panel/api/setting/apiTokens/delete/:id',
        summary: 'Permanently delete a token. Any caller using it stops authenticating immediately.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Token row ID.' },
        ],
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/apiTokens/setEnabled/:id',
        summary: 'Toggle a token enabled/disabled without deleting it. Disabled tokens are rejected by checkAPIAuth on the next request.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Token row ID.' },
          { name: 'enabled', in: 'body', type: 'boolean', desc: 'New enabled state.' },
        ],
        body: '{\n  "enabled": false\n}',
        response: '{\n  "success": true\n}',
      },
    ],
  },

  {
    id: 'xray-settings',
    title: 'Xray Settings',
    description:
      'Xray configuration template, outbound management, Warp/Nord integration, and config testing. All endpoints under /panel/api/xray.',
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/xray/',
        summary: 'Return the Xray config template (JSON string), available inbound tags, client reverse tags, and the configured outbound test URL in one response.',
        response: '{\n  "success": true,\n  "obj": {\n    "xraySetting": "{...raw xray config...}",\n    "inboundTags": "[\\"in-443-tcp\\"]",\n    "clientReverseTags": "[]",\n    "outboundTestUrl": "https://www.google.com/generate_204"\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/getDefaultJsonConfig',
        summary: 'Return the built-in default Xray config shipped with the panel (identical to /panel/api/setting/getDefaultJsonConfig).',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/getOutboundsTraffic',
        summary: 'Return traffic statistics for every outbound. Each outbound shows up/down/total counters.',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/getXrayResult',
        summary: 'Return the most recent Xray process stdout/stderr output. Useful to check for startup errors or runtime warnings.',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/update',
        summary: 'Save the Xray JSON config template and optionally the outbound test URL. Both are sent as form fields.',
        params: [
          { name: 'xraySetting', in: 'body (form)', type: 'string', desc: 'Full Xray JSON config template.' },
          { name: 'outboundTestUrl', in: 'body (form)', type: 'string', desc: 'URL used for outbound reachability tests. Defaults to https://www.google.com/generate_204.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/warp/:action',
        summary: 'Manage Cloudflare Warp integration. The action parameter selects the operation.',
        params: [
          { name: 'action', in: 'path', type: 'string', desc: 'data — return Warp stats (quota, remaining). del — delete Warp data. config — return current Warp config. reg — register a new Warp endpoint (sends privateKey, publicKey). license — set a Warp+ license key (sends license).' },
          { name: 'privateKey', in: 'body (form)', type: 'string', desc: 'Required when action=reg.' },
          { name: 'publicKey', in: 'body (form)', type: 'string', desc: 'Required when action=reg.' },
          { name: 'license', in: 'body (form)', type: 'string', desc: 'Required when action=license.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/nord/:action',
        summary: 'Manage NordVPN integration. The action parameter selects the operation.',
        params: [
          { name: 'action', in: 'path', type: 'string', desc: 'countries — list available countries. servers — list servers in a country (sends countryId). reg — get NordVPN credentials (sends token). setKey — store NordVPN API key (sends key). data — return current NordVPN connection data. del — delete NordVPN data.' },
          { name: 'countryId', in: 'body (form)', type: 'string', desc: 'Required when action=servers.' },
          { name: 'token', in: 'body (form)', type: 'string', desc: 'Required when action=reg.' },
          { name: 'key', in: 'body (form)', type: 'string', desc: 'Required when action=setKey.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/resetOutboundsTraffic',
        summary: 'Reset traffic counters for a specific outbound by tag.',
        params: [
          { name: 'tag', in: 'body (form)', type: 'string', desc: 'Outbound tag to reset (e.g. "proxy", "direct").' },
        ],
        body: 'tag=proxy',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/testOutbound',
        summary: 'Test an outbound configuration. Sends the outbound JSON (required), optionally all outbounds (to resolve sockopt.dialerProxy dependencies), and a mode flag.',
        params: [
          { name: 'outbound', in: 'body (form)', type: 'string', desc: 'JSON-encoded single outbound to test (required).' },
          { name: 'allOutbounds', in: 'body (form)', type: 'string', desc: 'JSON array of all outbounds — used to resolve dialerProxy chains.' },
          { name: 'mode', in: 'body (form)', type: 'string', desc: '"tcp" for a fast dial-only probe (parallel-safe), "real" for a real-delay probe whose delay is the full request time including tunnel establishment. Default/empty uses a full HTTP probe reporting the warm per-request round-trip. Both HTTP variants run through a temp xray instance.' },
        ],
        body: 'outbound={"protocol":"freedom","settings":{}}&mode=tcp',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/testOutbounds',
        summary: 'Test a batch of outbounds (max 50) through one shared temp xray instance. Returns an array of results in input order, each with the outbound tag, delay, HTTP status and a connect/TLS/TTFB timing breakdown.',
        params: [
          { name: 'outbounds', in: 'body (form)', type: 'string', desc: 'JSON array of outbound configs to test (required).' },
          { name: 'allOutbounds', in: 'body (form)', type: 'string', desc: 'JSON array of all outbounds — used to resolve dialerProxy chains.' },
          { name: 'mode', in: 'body (form)', type: 'string', desc: '"tcp" for fast dial-only probes (UDP-transport outbounds are still probed over HTTP), "real" for real-delay probes whose delay is the full request time including tunnel establishment. Default/empty routes an HTTP request through each outbound and reports the warm per-request round-trip.' },
        ],
        body: 'outbounds=[{"tag":"direct","protocol":"freedom","settings":{}}]&mode=http',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/balancerStatus',
        summary: 'Live state of routing balancers in the running core (RoutingService.GetBalancerInfo): current override and the targets the strategy prefers. Returns a map keyed by balancer tag.',
        params: [
          { name: 'tags', in: 'body (form)', type: 'string', desc: 'Comma-separated balancer tags to query (e.g. "b1,b2").' },
        ],
        body: 'tags=b1,b2',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/balancerOverride',
        summary: 'Force a balancer in the running core to always pick one outbound (RoutingService.OverrideBalancerTarget). Applied live without a restart; cleared automatically when Xray restarts.',
        params: [
          { name: 'tag', in: 'body (form)', type: 'string', desc: 'Balancer tag (required).' },
          { name: 'target', in: 'body (form)', type: 'string', desc: 'Outbound tag to force. Empty clears the override and returns control to the strategy.' },
        ],
        body: 'tag=b1&target=proxy',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/routeTest',
        summary: 'Ask the running core which outbound its router would pick for a synthetic connection (RoutingService.TestRoute). No traffic is sent.',
        params: [
          { name: 'domain', in: 'body (form)', type: 'string', desc: 'Target domain. Either domain or ip is required.' },
          { name: 'ip', in: 'body (form)', type: 'string', desc: 'Target IP. Either domain or ip is required.' },
          { name: 'port', in: 'body (form)', type: 'number', desc: 'Target port (optional).' },
          { name: 'network', in: 'body (form)', type: 'string', desc: '"tcp" (default) or "udp".' },
          { name: 'inboundTag', in: 'body (form)', type: 'string', desc: 'Simulate arrival on this inbound (optional).' },
          { name: 'protocol', in: 'body (form)', type: 'string', desc: 'Sniffed protocol such as http, tls, bittorrent (optional).' },
          { name: 'email', in: 'body (form)', type: 'string', desc: 'User attribution for user-based rules (optional).' },
        ],
        body: 'domain=example.com&port=443&network=tcp',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/outbound-subs',
        summary: 'List all outbound subscriptions (remote URLs that supply additional outbounds), newest first.',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/outbound-subs',
        summary: 'Create an outbound subscription. The URL is fetched, parsed into outbounds with stable tags, and merged additively into the running Xray config.',
        params: [
          { name: 'remark', in: 'body (form)', type: 'string', desc: 'Optional display label.' },
          { name: 'url', in: 'body (form)', type: 'string', desc: 'Subscription URL (required). Must be a public http(s) address; private/internal targets are blocked unless allowPrivate is true.' },
          { name: 'tagPrefix', in: 'body (form)', type: 'string', desc: 'Prefix for generated outbound tags. Defaults to "sub<id>-".' },
          { name: 'updateInterval', in: 'body (form)', type: 'integer', desc: 'Seconds between auto-refreshes. Default 600.' },
          { name: 'enabled', in: 'body (form)', type: 'boolean', desc: 'Whether the subscription is active. Default true.' },
          { name: 'allowPrivate', in: 'body (form)', type: 'boolean', desc: 'Allow the URL to point at a private/internal/loopback address (localhost/LAN). Default false (SSRF guard blocks private targets).' },
          { name: 'prepend', in: 'body (form)', type: 'boolean', desc: 'Place this subscription\'s outbounds before the manual template outbounds (so one can become the default). Default false.' },
        ],
      },
      {
  requestBodyRequired: true,

        method: 'POST',
        path: '/panel/api/xray/outbound-subs/:id',
        summary: 'Update an existing outbound subscription by id. Accepts the same form fields as create.',
        params: [ { name: 'id', in: 'path', type: 'integer' },
  { name: 'remark', in: 'body (form)', type: 'string', optional: true },
  { name: 'url', in: 'body (form)', type: 'string' },
  { name: 'tagPrefix', in: 'body (form)', type: 'string', optional: true },
  { name: 'enabled', in: 'body (form)', type: 'boolean', optional: true },
  { name: 'allowPrivate', in: 'body (form)', type: 'boolean', optional: true },
  { name: 'allowInsecure', in: 'body (form)', type: 'boolean', optional: true },
  { name: 'prepend', in: 'body (form)', type: 'boolean', optional: true },
  { name: 'updateInterval', in: 'body (form)', type: 'integer', optional: true, desc: 'Refresh interval in seconds; defaults to 600.' } ],
      },
      {
        method: 'DELETE',
        path: '/panel/api/xray/outbound-subs/:id',
        summary: 'Delete an outbound subscription by id.',
        params: [
          { name: 'id', in: 'path', type: 'integer', desc: 'Subscription id.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/outbound-subs/:id/del',
        summary: 'Delete an outbound subscription by id (POST alias of DELETE for clients that cannot send DELETE).',
        params: [
          { name: 'id', in: 'path', type: 'integer', desc: 'Subscription id.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/outbound-subs/:id/refresh',
        summary: 'Force an immediate re-fetch of the subscription and return the parsed outbounds. Signals Xray to reload.',
        params: [
          { name: 'id', in: 'path', type: 'integer', desc: 'Subscription id.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/outbound-subs/:id/move',
        summary: 'Reorder a subscription one step up or down in priority (controls its position in the merged outbounds).',
        params: [
          { name: 'id', in: 'path', type: 'integer', desc: 'Subscription id.' },
          { name: 'dir', in: 'body (form)', type: 'string', desc: '"up" to raise priority, anything else to lower it.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/outbound-subs/parse',
        summary: 'Preview a subscription URL: fetch and parse it into outbounds without persisting anything.',
        params: [
          { name: 'url', in: 'body (form)', type: 'string', desc: 'Subscription URL to preview (required).' },
        ],
      },
    ],
  },

  {
    id: 'subscription',
    title: 'Subscription Server',
    description:
      'A separate HTTP/HTTPS server that serves proxy subscription links (standard, JSON, and Clash) to clients. The server listens on its own port (default 2096) and is configured in Settings → Subscription. Paths are configurable; defaults are shown below. Subscription responses include traffic/expiry headers; individual configuration-file downloads carry an attachment filename.',
    subHeader: [
      { name: 'Subscription-Userinfo', desc: 'Traffic and expiry: <code>upload=N; download=N; total=N; expire=TS</code>' },
      { name: 'Profile-Title', desc: 'Base64-encoded subscription display name' },
      { name: 'Profile-Web-Page-Url', desc: 'Link to the subscription info page' },
      { name: 'Support-Url', desc: 'Support contact URL configured in settings' },
      { name: 'Profile-Update-Interval', desc: 'Suggested polling interval in minutes (e.g. <code>10</code>)' },
      { name: 'Announce', desc: 'Base64-encoded announcement string' },
      { name: 'Routing-Enable', desc: '<code>true</code> or <code>false</code> — whether routing rules are included' },
      { name: 'Routing', desc: 'Global routing rules for client apps that support them (e.g. Happ)' },
    ],
    endpoints: [
      {
        method: 'GET',
        path: '/{subPath}:subid',
        summary: 'Return subscription links, base64-encoded when enabled in settings, for every client matching the subscription ID on an enabled inbound. A disabled client is still listed — its credential is stripped from the running core, so the link cannot connect; use the {{STATUS}} remark token to label it. Accept: text/html or ?html=1 selects the info page; ?format=info returns its JSON view-model without links. Configured user-agent detection can select JSON or Clash YAML. Served by the separate subscription listener; default path: /sub/:subid.',
        responseContent: {
          'text/plain': { schema: { type: 'string' } },
          'text/html': { schema: { type: 'string' } },
          'application/json': { schema: { oneOf: [{ type: 'object', additionalProperties: true }, { type: 'array', items: { type: 'object', additionalProperties: true } }] } },
          'application/yaml': { schema: { type: 'string' } },
        },
        params: [
          { name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' },
          { name: 'format', in: 'query', type: 'string', optional: true, desc: 'Set to "info" to get the subscription status view-model as JSON instead of the links.' },
        ],
      },
      {
        method: 'GET',
        path: '/{subPath}:subid/file/:inboundId',
        summary:
          'Download the configuration file for one inbound of the subscription, as an attachment — the .conf a kernel WireGuard or AmneziaWG client imports. The subscription ID is the only credential. Anything that is not file-shaped, or that the ID does not own, answers 404 with no body, indistinguishable from a subscription that does not exist, so a guessed ID confirms nothing. Default path: /sub/:subid/file/:inboundId.',
        responseContent: { 'application/octet-stream': { schema: { type: 'string', format: 'binary' } } },
        params: [
          { name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' },
          { name: 'inboundId', in: 'path', type: 'number', desc: 'The inbound whose client file to download; it must hand out a file-shaped config.' },
        ],
      },
      {
        method: 'GET',
        path: '/{jsonPath}:subid',
        summary: 'Return JSON proxy configuration as an object or array according to subscription settings. Default media type is text/plain; ?view=raw downloads application/json, and browser HTML requests select the info page. Disabled clients may be listed but cannot connect. Requires JSON subscription enabled on the separate subscription listener. Default path: /json/:subid.',
        responseContent: {
          'text/plain': { schema: { type: 'string' } },
          'application/json': { schema: { oneOf: [{ type: 'object', additionalProperties: true }, { type: 'array', items: { type: 'object', additionalProperties: true } }] } },
          'text/html': { schema: { type: 'string' } },
        },
        params: [
          { name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' },
          { name: 'view', in: 'query', type: 'string', optional: true, desc: 'Set to raw to download the JSON document with application/json media type.' },
        ],
      },
      {
        method: 'GET',
        path: '/{clashPath}:subid',
        summary: 'Return a Clash/Mihomo-compatible YAML config, including configured global Clash routing rules. Browser HTML requests select the info page; ?view=raw downloads YAML directly. Requires Clash subscription enabled on the separate subscription listener. Default path: /clash/:subid.',
        responseContent: {
          'application/yaml': { schema: { type: 'string' } },
          'text/html': { schema: { type: 'string' } },
        },
        params: [
          { name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' },
          { name: 'view', in: 'query', type: 'string', optional: true, desc: 'Set to raw to download YAML directly instead of the browser info page.' },
        ],
      },
    ],
  },

  {
    id: 'websocket',
    title: 'WebSocket',
    description:
      'Real-time status updates via WebSocket. Connect once at <code>ws://<panel>/ws</code> to receive a stream of JSON messages without polling. Requires an authenticated session cookie (Bearer token auth is not supported). Every message is the same envelope: <code>type</code> identifies the payload shape, <code>payload</code> carries it, and <code>time</code> is the send time in Unix milliseconds.',
    endpoints: [
      {
        method: 'GET',
        path: '/ws',
        summary: 'Upgrade an HTTP connection to a WebSocket. Requires an authenticated session cookie (Bearer token auth is not supported here). Returns 101 Switching Protocols on success. The server then pushes JSON messages described below.',
      },
      {
        method: 'WS',
        path: '→ type: status',
        summary: 'Server health snapshot pushed every 2 seconds. The payload contains CPU, memory, swap, disk, network IO, load, and Xray state — same shape as <code>GET /panel/api/server/status</code>.',
        response: '{\n  "type": "status",\n  "payload": { "cpu": 12.5, "mem": { "current": 2147483648, "total": 8589934592 }, "xray": { "state": "running" } },\n  "time": 1725000000000\n}',
      },
      {
        method: 'WS',
        path: '→ type: xray_state',
        summary: 'Xray process state change. Fired when Xray starts, stops, or encounters an error; <code>state</code> is <code>running</code>, <code>stop</code> or <code>error</code>, and <code>errorMsg</code> carries the reason when it is <code>error</code>.',
        response: '{\n  "type": "xray_state",\n  "payload": { "state": "running", "errorMsg": "" },\n  "time": 1725000000000\n}',
      },
      {
        method: 'WS',
        path: '→ type: notification',
        summary: 'In-panel toast notification. Fired on Xray stop/restart, DB import, panel restart, etc. <code>level</code> is <code>success</code>, <code>info</code>, <code>warning</code> or <code>error</code>.',
        response: '{\n  "type": "notification",\n  "payload": { "title": "Xray service restarted", "message": "Xray service has been restarted successfully", "level": "success" },\n  "time": 1725000000000\n}',
      },
      {
        method: 'WS',
        path: '→ type: invalidate',
        summary: 'Instructs the UI to re-fetch a resource. Fired when another admin session modifies data (e.g. toggling inbound enable). The payload names the message type whose cached data is stale: <code>inbounds</code>, <code>outbounds</code>, <code>nodes</code> or <code>clients</code>.',
        response: '{\n  "type": "invalidate",\n  "payload": { "type": "inbounds" },\n  "time": 1725000000000\n}',
      },
    ],
  },









{
  id: 'resellers',
  title: 'Resellers',
  description: 'Administrator account management and owner-scoped reseller clients, inbound grants, budgets, private plans and tokens.',
  endpoints: [
    {
      method: 'GET',
      path: '/panel/api/me',
      summary: 'Read the authenticated identity, role, permissions and home route',
      responseSchema: 'SignedInUser',
      responseSchemaArray: false,
      params: []
    },
    {
      method: 'GET',
      path: '/panel/api/resellers',
      summary: 'List reseller accounts and actual usage budgets',
      responseSchema: 'ResellerView',
      responseSchemaArray: true,
      params: []
    },
    {
      method: 'GET',
      path: '/panel/api/resellers/:id',
      summary: 'Read one reseller account',
      responseSchema: 'ResellerView',
      responseSchemaArray: false,
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'GET',
      path: '/panel/api/resellers/:id/clients',
      summary: 'List clients owned by one reseller',
      responseSchema: 'ResellerClientPage',
      responseSchemaArray: false,
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
  requestRequired: [ 'name', 'username', 'password', 'inboundIds' ],

      method: 'POST',
      path: '/panel/api/resellers',
      summary: 'Create reseller credentials, budget and inbound grants',
      responseSchema: 'ResellerView',
      responseSchemaArray: false,
      requestSchema: 'ResellerInput',
      params: []
    },
    {
  requestRequired: [ 'name', 'username', 'inboundIds' ],

      method: 'POST',
      path: '/panel/api/resellers/:id/update',
      summary: 'Update reseller credentials, permissions and grants',
      responseSchema: 'ResellerView',
      responseSchemaArray: false,
      requestSchema: 'ResellerInput',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
  requestRequired: [ 'suspended' ],
  requestProperties: { suspended: { nullable: false } },

      method: 'POST',
      path: '/panel/api/resellers/:id/suspend',
      summary: 'Suspend or reactivate a reseller and its previously enabled clients',
      requestSchema: 'ResellerSuspendRequest',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
  requestRequired: [],
  description: 'At least one of traffic, days, or clients must be positive. Other fields may be omitted. Unlimited budget axes cannot be topped up.',
  requestProperties: { traffic: { minimum: 0 }, days: { minimum: 0, maximum: 36500 }, clients: { minimum: 0 } },

      method: 'POST',
      path: '/panel/api/resellers/:id/top-up',
      summary: 'Add traffic capacity, account days or client slots',
      requestSchema: 'ResellerTopUpRequest',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/dashboard',
      summary: 'Read owned account usage, client attention, grants and quota allocation history',
      responseSchema: 'ResellerDashboardView',
      responseSchemaArray: false,
      params: []
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/inbounds',
      summary: 'Read granted inbound labels and owned counts without configuration or keys',
      responseSchema: 'ResellerInboundView',
      responseSchemaArray: true,
      params: []
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/clients',
      summary: 'Page through owned clients',
      responseSchema: 'ResellerClientPage',
      responseSchemaArray: false,
      params: []
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/clients',
      summary: 'Provision an owned client within the reseller account constraints',
      responseSchema: 'ResellerMutationResult',
      responseSchemaArray: false,
      requestSchema: 'ResellerClientInput',
      params: []
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/clients/:id/update',
      summary: 'Edit an owned client with extension, top-up and suspension permissions',
      responseSchema: 'ResellerMutationResult',
      responseSchemaArray: false,
      requestSchema: 'ResellerClientInput',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/clients/:id/delete',
      summary: 'Delete an owned client without refunding account usage',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/clients/:id/reset',
      summary: "Reset an owned client's traffic counter without refunding account usage",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/clients/:id/links',
      summary: 'Read connection links for one owned client',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/tokens',
      summary: "List the caller's reseller token metadata",
      responseSchema: 'ResellerTokenView',
      responseSchemaArray: true,
      params: []
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/tokens',
      summary: 'Create a token bound to the caller and current login epoch',
      responseSchema: 'ResellerTokenView',
      responseSchemaArray: false,
      requestSchema: 'ResellerTokenRequest',
      params: []
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/tokens/:id/delete',
      summary: 'Revoke one token owned by the caller',
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer',
          desc: 'Owned resource ID'
        }
      ]
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/password',
      summary: "Change the caller's password and invalidate its sessions and tokens",
      requestSchema: 'ResellerPasswordRequest',
      params: []
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/plans/list',
      summary: "Plan list within the authenticated owner's scope.",
      params: [],
      responseSchema: 'Policy',
      responseSchemaArray: true
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/plans/summary',
      summary: "Plan summary within the authenticated owner's scope.",
      params: [],
      responseSchema: 'PlanSummary',
      responseSchemaArray: false
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/plans/get/:id',
      summary: "Plan get within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        }
      ],
      responseSchema: 'Policy',
      responseSchemaArray: false
    },
    {
      method: 'GET',
      path: '/panel/api/reseller/plans/members/:id',
      summary: "Plan members within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        },
        {
          name: 'page',
          in: 'query',
          type: 'integer',
          optional: true,
          defaultValue: 1
        },
        {
          name: 'pageSize',
          in: 'query',
          type: 'integer',
          optional: true,
          defaultValue: 20
        }
      ],
      responseSchema: 'PlanMemberPage',
      responseSchemaArray: false
    },
    {
  requestSchema: 'Policy',
  requestRequired: [ 'name' ],
  requestProperties: { tiers:
   { type: 'array',
     items:
      { type: 'object',
        properties:
         { fromBytes: { type: 'integer', format: 'int64' }, upBps: { type: 'integer', format: 'int64' }, downBps: { type: 'integer', format: 'int64' } } } } },

      method: 'POST',
      path: '/panel/api/reseller/plans/add',
      summary: "Plan add within the authenticated owner's scope.",
      params: [
        {
          name: 'description',
          in: 'body',
          type: 'string',
          optional: true
        },
        {
          name: 'quotaBytes',
          in: 'body',
          type: 'integer',
          optional: true,
          desc: 'Null is unmanaged; zero is unlimited.'
        },
        {
          name: 'durationDays',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'limitIp',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'inboundIds',
          in: 'body',
          type: 'integer[]',
          optional: true
        },
        {
          name: 'startOnFirstUse',
          in: 'body',
          type: 'boolean',
          optional: true
        },
        {
          name: 'autoRenewDays',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'resetCycle',
          in: 'body',
          type: 'string',
          optional: true,
          desc: 'none, weekly or monthly'
        },
        {
          name: 'onDepletion',
          in: 'body',
          type: 'string',
          optional: true,
          desc: 'disable, throttle or notify; runtime capability restrictions are enforced.'
        }
      ],
      responseSchema: 'Policy',
      responseSchemaArray: false
    },
    {
  requestSchema: 'Policy',
  requestRequired: [],
  requestProperties: { tiers:
   { type: 'array',
     items:
      { type: 'object',
        properties:
         { fromBytes: { type: 'integer', format: 'int64' }, upBps: { type: 'integer', format: 'int64' }, downBps: { type: 'integer', format: 'int64' } } } } },

      method: 'POST',
      path: '/panel/api/reseller/plans/update/:id',
      summary: "Plan update within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        },
        {
          name: 'description',
          in: 'body',
          type: 'string',
          optional: true
        },
        {
          name: 'quotaBytes',
          in: 'body',
          type: 'integer',
          optional: true,
          desc: 'Null is unmanaged; zero is unlimited.'
        },
        {
          name: 'durationDays',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'limitIp',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'inboundIds',
          in: 'body',
          type: 'integer[]',
          optional: true
        },
        {
          name: 'startOnFirstUse',
          in: 'body',
          type: 'boolean',
          optional: true
        },
        {
          name: 'autoRenewDays',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'resetCycle',
          in: 'body',
          type: 'string',
          optional: true,
          desc: 'none, weekly or monthly'
        },
        {
          name: 'onDepletion',
          in: 'body',
          type: 'string',
          optional: true,
          desc: 'disable, throttle or notify; runtime capability restrictions are enforced.'
        }
      ],
      responseSchema: 'Policy',
      responseSchemaArray: false
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/plans/archive/:id',
      summary: "Plan archive within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        },
        {
          name: 'archived',
          in: 'body',
          type: 'boolean'
        }
      ],
      responseSchema: 'Policy',
      responseSchemaArray: false
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/plans/duplicate/:id',
      summary: "Plan duplicate within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        },
        {
          name: 'name',
          in: 'body',
          type: 'string'
        }
      ],
      responseSchema: 'Policy',
      responseSchemaArray: false
    },
    {
      method: 'POST',
      path: '/panel/api/reseller/plans/del/:id',
      summary: "Plan del within the authenticated owner's scope.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'integer'
        }
      ]
    }
  ]
},
{
  id: 'core-configs',
  title: 'Core Configurations',
  description: 'Read persisted per-core inbound configuration and validate supported documents with the installed core. Administrator write access is required.',
  endpoints: [
    {
      method: 'GET',
      path: '/panel/api/core-config/:id',
      summary: "Read this host's actual persisted configuration for a registered core.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'string'
        }
      ],
      responseSchema: 'CoreConfigView'
    },
    {
      method: 'POST',
      path: '/panel/api/core-config/:id/validate',
      summary: "Test an Xray template with its installed binary or validate a protocol's settings with the owning adapter.",
      params: [
        {
          name: 'id',
          in: 'path',
          type: 'string'
        },
        {
          name: 'kind',
          in: 'body',
          type: 'string',
          optional: true,
          desc: 'Protocol kind; omit only for Xray template testing.'
        },
        {
          name: 'document',
          in: 'body',
          type: 'string',
          desc: 'JSON object encoded as a string'
        }
      ]
    }
  ]
},
{
  id: 'activity',
  title: 'Activity',
  description: 'Retained account-visible activity, per-user read state and current alerts. History is limited to 30 days and 10,000 events.',
  endpoints: [
    {
      method: 'GET',
      path: '/panel/api/activity',
      summary: 'Read retained events visible to this account, with per-user read state and cursor pagination.',
      responseSchema: 'ActivityPage',
      params: [
        {
          name: 'before',
          in: 'query',
          type: 'integer',
          optional: true,
          defaultValue: 0
        },
        {
          name: 'limit',
          in: 'query',
          type: 'integer',
          optional: true,
          defaultValue: 100
        }
      ]
    },
    {
      method: 'GET',
      path: '/panel/api/activity/alerts',
      summary: 'Compute current alerts for this account; administrator health data is never returned to a reseller.',
      responseSchema: 'ActivityView',
      responseSchemaArray: true,
      params: []
    },
    {
      method: 'POST',
      path: '/panel/api/activity/read',
      summary: 'Mark an event or visible events through an id as read for the authenticated user only.',
      params: [
        {
          name: 'id',
          in: 'body',
          type: 'integer',
          optional: true
        },
        {
          name: 'throughId',
          in: 'body',
          type: 'integer',
          optional: true,
          desc: 'Use exactly one of id or throughId; newly arriving events remain unread.'
        }
      ]
    }
  ]
},
];
