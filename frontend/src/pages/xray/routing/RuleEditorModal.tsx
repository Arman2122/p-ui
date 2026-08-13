import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Form, Input, Modal, Select, Switch, Tag, Typography } from 'antd';

import {
  CRITERIA_FIELDS,
  criteriaFromForm,
  criteriaToForm,
  ingressIdsToArray,
  type RoutingRulePayload,
  type RoutingRuleRecord,
  type RoutingSubjectView,
} from '@/schemas/api/routing';

interface RuleEditorModalProps {
  open: boolean;
  rule: RoutingRuleRecord | null;
  subjects: RoutingSubjectView[];
  outboundTags: string[];
  balancerTags: string[];
  saving: boolean;
  onClose: () => void;
  onSave: (payload: RoutingRulePayload) => void;
}

/*
The rule editor. "From" lists every inbound, an unroutable one disabled with its
reason; "Send through" is one picker over outbounds, balancers, direct and block.

Criteria are gated by the INTERSECTION of the selected subjects' masks, because
one rule carries one set of criteria and the narrowest subject decides what can
match. A gated field is disabled with the reason, never hidden.
*/
export default function RuleEditorModal({
  open, rule, subjects, outboundTags, balancerTags, saving, onClose, onSave,
}: RuleEditorModalProps) {
  const { t } = useTranslation();
  const [enable, setEnable] = useState(true);
  const [remark, setRemark] = useState('');
  const [scope, setScope] = useState('selected');
  const [ingressIds, setIngressIds] = useState<number[]>([]);
  const [dest, setDest] = useState('direct');
  const [criteria, setCriteria] = useState<Record<string, string>>({});

  useEffect(() => {
    if (!open) return;
    setEnable(rule?.enable ?? true);
    setRemark(rule?.remark ?? '');
    setScope(rule?.ingressScope ?? 'selected');
    setIngressIds(rule ? ingressIdsToArray(rule.ingressIds) : []);
    setCriteria(rule ? criteriaToForm(rule.criteria) : {});
    if (!rule) {
      setDest('direct');
      return;
    }
    if (rule.destKind === 'outbound' || rule.destKind === 'balancer') {
      setDest(`${rule.destKind}:${rule.destTag}`);
    } else {
      setDest(rule.destKind);
    }
  }, [open, rule]);

  const selected = useMemo(
    () => (scope === 'all' ? subjects : subjects.filter((s) => ingressIds.includes(s.inboundId))),
    [scope, subjects, ingressIds],
  );

  /* The intersection: a rule naming an Xray inbound and a kernel inbound gets
     the narrower mask, so "the same criteria on both" is what the form enforces. */
  const allowed = useMemo(() => {
    if (selected.length === 0) return new Set<string>(CRITERIA_FIELDS);
    return selected.reduce<Set<string>>((acc, subject) => {
      const mask = new Set(subject.criteriaMask);
      return new Set([...acc].filter((field) => mask.has(field)));
    }, new Set<string>(selected[0]?.criteriaMask ?? []));
  }, [selected]);

  const dropped = useMemo(
    () => Object.keys(criteria).filter((key) => (criteria[key] ?? '').trim() !== '' && !allowed.has(key)),
    [criteria, allowed],
  );

  const submit = () => {
    const [kind, tag] = dest.includes(':') ? dest.split(':') : [dest, ''];
    onSave({
      enable,
      remark,
      ingressScope: scope,
      ingressIds: JSON.stringify(scope === 'all' ? [] : ingressIds),
      destKind: kind,
      destTag: tag,
      destExitId: null,
      criteria: criteriaFromForm(
        Object.fromEntries(Object.entries(criteria).filter(([key]) => allowed.has(key))),
      ),
      inspect: rule?.inspect ?? false,
    });
  };

  const destOptions = [
    {
      label: t('pages.xray.routing.dest.outbounds'),
      options: outboundTags.filter(Boolean).map((tag) => ({ label: tag, value: `outbound:${tag}` })),
    },
    {
      label: t('pages.xray.routing.dest.balancers'),
      options: balancerTags.filter(Boolean).map((tag) => ({ label: tag, value: `balancer:${tag}` })),
    },
    {
      label: t('pages.xray.routing.dest.builtin'),
      options: [
        { label: t('pages.xray.routing.dest.direct'), value: 'direct' },
        { label: t('pages.xray.routing.dest.block'), value: 'block' },
      ],
    },
  ];

  return (
    <Modal
      open={open}
      title={rule ? t('pages.xray.routing.editRule') : t('pages.xray.routing.addRule')}
      onCancel={onClose}
      onOk={submit}
      confirmLoading={saving}
      okButtonProps={{ disabled: scope === 'selected' && ingressIds.length === 0 }}
      width={640}
      destroyOnHidden
    >
      <Form layout="vertical">
        <Form.Item label={t('pages.xray.routing.from')} htmlFor="ruleFrom">
          <Select
            id="ruleFrom"
            mode="multiple"
            value={scope === 'all' ? [] : ingressIds}
            disabled={scope === 'all'}
            placeholder={t('pages.xray.routing.fromPlaceholder')}
            onChange={(value: number[]) => setIngressIds(value)}
            options={subjects.map((subject) => ({
              value: subject.inboundId,
              label: subject.tag,
              disabled: !subject.routable,
              blockedKey: subject.blockedKey,
            }))}
            optionRender={(option) => {
              const blockedKey = (option.data as { blockedKey?: string }).blockedKey;
              return (
                <div>
                  <div>{option.label}</div>
                  {blockedKey && (
                    <Typography.Text type="secondary" style={{ fontSize: 12, whiteSpace: 'normal' }}>
                      {t(blockedKey)}
                    </Typography.Text>
                  )}
                </div>
              );
            }}
          />
        </Form.Item>

        <Form.Item>
          <Switch
            checked={scope === 'all'}
            onChange={(checked) => setScope(checked ? 'all' : 'selected')}
          />{' '}
          {t('pages.xray.routing.scopeAll')}
        </Form.Item>

        <Form.Item label={t('pages.xray.routing.to')} htmlFor="ruleTo">
          <Select id="ruleTo" value={dest} onChange={setDest} options={destOptions} />
        </Form.Item>

        <Form.Item label={t('pages.xray.routing.remark')} htmlFor="ruleRemark">
          <Input id="ruleRemark" value={remark} onChange={(e) => setRemark(e.target.value)} />
        </Form.Item>

        {dropped.length > 0 && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message={t('pages.xray.routing.dropCriteriaConfirm')}
            description={t('pages.xray.routing.dropCriteriaConfirmDesc', { fields: dropped.join(', ') })}
          />
        )}

        <Typography.Text strong>{t('pages.xray.routing.criteria')}</Typography.Text>
        {CRITERIA_FIELDS.map((field) => {
          const off = !allowed.has(field);
          return (
            <Form.Item
              key={field}
              label={field}
              htmlFor={`criterion-${field}`}
              extra={off ? t('pages.xray.routing.criterionUnavailable') : undefined}
            >
              <Input
                id={`criterion-${field}`}
                disabled={off}
                value={criteria[field] ?? ''}
                onChange={(e) => setCriteria((prev) => ({ ...prev, [field]: e.target.value }))}
              />
            </Form.Item>
          );
        })}

        <Form.Item>
          <Switch checked={enable} onChange={setEnable} /> <Tag>{t('enable')}</Tag>
        </Form.Item>
      </Form>
    </Modal>
  );
}
