import { describe, it, expect } from 'vitest';

import { buildInboundTagOptions } from '@/pages/xray/routing/helpers';
import enUS from '../../../internal/web/translation/en-US.json';
import faIR from '../../../internal/web/translation/fa-IR.json';

const SUBJECTS = [
  { id: 1, tag: 'vless-in', remark: 'main', protocol: 'vless', routable: true },
  {
    id: 2, tag: 'wg-home', remark: 'tunnel', protocol: 'wgkernel', routable: false,
    reasonKey: 'pages.xray.subjects.reasonNoXrayInbound',
  },
];

/*
The editor offered every tag in the inbounds table, so a rule naming a wgkernel
inbound saved cleanly and then never matched a packet -- confirmed on a real
host, where the rule was accepted and traffic was entirely unaffected.
*/
describe('buildInboundTagOptions', () => {
  it('disables a tag the router provably never sees, and carries its reason', () => {
    const options = buildInboundTagOptions(SUBJECTS, []);

    expect(options).toEqual([
      { value: 'vless-in', disabled: false, reasonKey: undefined },
      { value: 'wg-home', disabled: true, reasonKey: 'pages.xray.subjects.reasonNoXrayInbound' },
    ]);
  });

  /* Template tags are real xray tags with no inbound row of their own, so they
     stay selectable -- the subjects query cannot see them at all. */
  it('keeps template-derived tags selectable', () => {
    const options = buildInboundTagOptions(SUBJECTS, ['dns-in', 'api']);

    expect(options.filter((o) => !o.disabled).map((o) => o.value)).toEqual(['vless-in', 'dns-in', 'api']);
  });

  /* Without this an operator opening an old rule loses its stored inbound on the
     next save -- the edit silently widens the rule to every inbound. */
  it('never drops a tag the rule being edited already carries', () => {
    const options = buildInboundTagOptions(SUBJECTS, [], ['gone-tag']);

    expect(options.map((o) => o.value)).toContain('gone-tag');
  });

  it('does not offer the same tag twice', () => {
    const options = buildInboundTagOptions(SUBJECTS, ['vless-in'], ['vless-in']);

    expect(options.filter((o) => o.value === 'vless-in')).toHaveLength(1);
  });
});

/*
Every reason the Go side can emit has to exist in BOTH locales, or the picker
renders a raw key where the explanation should be. The keys are declared as Go
constants, so this list is the frontend's half of that contract.
*/
describe('subject reason keys', () => {
  const REASONS = [
    'reasonNode', 'reasonDisabled', 'reasonNoXrayInbound', 'reasonBridgeOff', 'reasonUnknownCore',
  ] as const;

  it('resolves in en-US and fa-IR', () => {
    for (const key of [...REASONS, 'neverMatches', 'neverMatchesTip'] as const) {
      const en = (enUS.pages.xray.subjects as Record<string, string>)[key];
      const fa = (faIR.pages.xray.subjects as Record<string, string>)[key];
      expect(en, `en-US is missing ${key}`).toBeTruthy();
      expect(fa, `fa-IR is missing ${key}`).toBeTruthy();
    }
  });

  it('gives every disabled option a reason to show', () => {
    const options = buildInboundTagOptions(
      REASONS.map((reasonKey, i) => ({
        id: i, tag: `t${i}`, routable: false, reasonKey: `pages.xray.subjects.${reasonKey}`,
      })),
      [],
    );

    for (const option of options) {
      expect(option.disabled).toBe(true);
      const leaf = (option.reasonKey ?? '').split('.').pop() as string;
      expect((enUS.pages.xray.subjects as Record<string, string>)[leaf]).toBeTruthy();
    }
  });
});
