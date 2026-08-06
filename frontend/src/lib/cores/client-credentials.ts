import { CLIENT_CREDENTIAL_NAMES, type ClientCredentialName } from '@/generated/capabilities';
import type { CoreView } from '@/generated/zod';

/*
Which credential fields a client form must offer, asked of what the cores
declare rather than of the protocol.

Source of truth for the names: internal/core/credentials.go. A name this build
has no input for is simply not matched, so a newer master may declare more
without breaking an older panel.
*/

/* A kind no core declares keeps the three fields the form has always shown for
   every protocol, so an unknown or quarantined inbound stays editable. Typed
   against the generated vocabulary, so renaming a Go constant fails here. */
const UNDECLARED: ClientCredentialName[] = ['uuid', 'password', 'auth'];

const KNOWN = new Set<string>(CLIENT_CREDENTIAL_NAMES);

export function credentialsForKinds(
  cores: CoreView[] | undefined,
  kinds: string[],
): Set<ClientCredentialName> {
  const declared = new Map<string, string[]>();
  for (const core of cores ?? []) {
    for (const [kind, fields] of Object.entries(core.clientCredentials ?? {})) {
      if (fields.length > 0) declared.set(kind, fields);
    }
  }
  const wanted = new Set<ClientCredentialName>();
  for (const kind of kinds) {
    for (const name of declared.get(kind) ?? UNDECLARED) {
      if (KNOWN.has(name)) wanted.add(name as ClientCredentialName);
    }
  }
  return wanted;
}
