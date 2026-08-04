import { describe, expect, it } from 'vitest';

import { CAPABILITY_RULES } from '@/generated/capabilities';
import { resolveAll, type CapabilityFacts } from '@/lib/cores/capabilities';

import table from './golden/fixtures/capabilities/resolve.json';

/*
The cross-language weld for capability rules.

internal/core/capability_test.go generates this fixture from the Go rule table
and the Go evaluator's answers. Here the same facts run through the TypeScript
evaluator and must produce the same answers. Change a clause in Go without
regenerating, or change this evaluator, and one of these goes red.

Regenerate with: PUI_UPDATE_GOLDEN=1 go test ./internal/core/ -run Golden
*/

interface GoldenCase {
  name: string;
  facts: CapabilityFacts;
  expect: Record<string, boolean>;
}

const golden = table as unknown as {
  rules: Record<string, unknown>;
  cases: GoldenCase[];
};

describe('capability rules agree across Go and TypeScript', () => {
  it('the generated rule table matches the one the fixture was built from', () => {
    expect(JSON.parse(JSON.stringify(CAPABILITY_RULES))).toEqual(golden.rules);
  });

  it('covers a non-trivial matrix', () => {
    expect(golden.cases.length).toBeGreaterThan(100);
    expect(Object.keys(golden.rules).length).toBeGreaterThan(0);
  });

  it('reproduces every answer Go produced', () => {
    const mismatches: string[] = [];
    for (const testCase of golden.cases) {
      const got = resolveAll(testCase.facts);
      for (const [capability, want] of Object.entries(testCase.expect)) {
        if (got[capability] !== want) {
          mismatches.push(
            `${testCase.name}: ${capability} = ${String(got[capability])}, Go says ${String(want)}`,
          );
        }
      }
    }
    expect(mismatches).toEqual([]);
  });
});
