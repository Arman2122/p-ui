import { useId } from 'react';
import { Form, Input, Select } from 'antd';
import { useTranslation } from 'react-i18next';
import { DocText } from './ApiReference';
import { guidedBufferKey, guidedNumberValid, readGuidedJson, setGuidedJsonValue, type GuidedJsonField } from './guided-json';

export interface GuidedJsonBodyProps {
  fields: GuidedJsonField[];
  value: string;
  buffers: Record<string, string>;
  onChange: (json: string, buffers: Record<string, string>, field: string) => void;
  errorFor: (field: string) => string | undefined;
}

export default function GuidedJsonBody({ fields, value, buffers, onChange, errorFor }: GuidedJsonBodyProps) {
  const { t } = useTranslation();
  const id = useId();
  const members = readGuidedJson(value);
  if (!members) return null;
  function update(field: GuidedJsonField, rawValue: string | undefined, buffer?: string) {
    const nextBuffers = { ...buffers };
    if (buffer === undefined) delete nextBuffers[guidedBufferKey(field.name)];
    else nextBuffers[guidedBufferKey(field.name)] = buffer;
    const next = buffer === undefined ? setGuidedJsonValue(value, field.name, rawValue) : value;
    if (next !== undefined) onChange(next, nextBuffers, `$.${field.name}`);
  }
  return <Form component="div" layout="vertical" autoComplete="off" className="p-apirequest-guided" data-form-type="other">{fields.map((field, index) => {
    const member = members.find((item) => item.name === field.name);
    const raw = member?.rawValue;
    const literal: unknown = raw === undefined ? undefined : JSON.parse(raw);
    const fieldId = `${id}-${encodeURIComponent(field.name)}`;
    const error = errorFor(`$.${field.name}`);
    const secret = field.format === 'password' || /password|secret|token/i.test(field.name);
    const choices = field.choices ?? (field.type === 'boolean' ? [true, false] : undefined);
    const mode = raw === undefined ? 'omit' : literal === null ? 'null' : 'value';
    const control = choices ? <Select id={fieldId} aria-label={`body: ${field.name}`} value={raw ?? '__omit'}
      onChange={(next) => update(field, next === '__omit' ? undefined : next)} options={[
        { value: '__omit', label: t('apiUx.omit') },
        ...choices.map((choice) => ({ value: JSON.stringify(choice), label: typeof choice === 'string' ? choice : JSON.stringify(choice) })),
        ...(field.nullable && !choices.includes(null) ? [{ value: 'null', label: 'null' }] : []),
      ]} /> : <div className="p-apirequest-guided-control">
        <Input id={fieldId} name={`api-request-${id}-value-${index}`} aria-label={`body: ${field.name}`} autoComplete={secret ? 'new-password' : 'off'} type={secret ? 'password' : 'text'}
          data-lpignore="true" data-1p-ignore="true" data-bwignore="true" data-form-type="other"
          inputMode={field.type === 'integer' || field.type === 'number' ? 'decimal' : undefined}
          value={buffers[guidedBufferKey(field.name)] ?? (literal === null || literal === undefined ? '' : field.type === 'string' && typeof literal === 'string' ? literal : raw ?? '')}
          placeholder={mode === 'null' ? 'null' : field.type} disabled={mode === 'null'}
          onChange={(event) => {
            const text = event.target.value;
            if (field.type === 'string') update(field, JSON.stringify(text));
            else if (!text) update(field, undefined);
            else if (guidedNumberValid(text)) update(field, text);
            else update(field, undefined, text);
          }} />
        {(!field.required || field.nullable) && <Select aria-label={t('apiUx.presence', { name: field.name })} value={mode}
          onChange={(next) => update(field, next === 'omit' ? undefined : next === 'null' ? 'null' : field.type === 'string' ? '""' : '0')}
          options={[{ value: 'omit', label: t('apiUx.omit') }, { value: 'value', label: t('apiUx.value') }, ...(field.nullable ? [{ value: 'null', label: 'null' }] : [])]} />}
      </div>;
    return <div data-api-field={`$.${field.name}`} key={field.name}>
      <Form.Item htmlFor={fieldId} label={<span>{field.name}{field.required && <span className="p-req"> *</span>}</span>}
        validateStatus={error ? 'error' : undefined} help={error} extra={field.description ? <DocText text={field.description} /> : undefined}>
        {control}
      </Form.Item>
    </div>;
  })}</Form>;
}
