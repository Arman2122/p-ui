import type { IntentResult, RoutingRulePayload } from '@/schemas/api/routing';

/*
Unwraps a ruleToIntent result that is expected to resolve.

ruleToIntent refuses rather than returning a payload when a named inbound does
not resolve: inferring the scope from what survived is what let a rule written
for one inbound come back applying to all of them. A test that means to exercise
the resolving path says so here rather than asserting through the union.
*/
export function expectResolved(result: IntentResult): RoutingRulePayload {
  if (!result.ok) {
    throw new Error(`ruleToIntent refused; unresolved: ${result.unresolved.join(', ')}`);
  }
  return result.payload;
}
