import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Form, Input, InputNumber, Select } from 'antd';

import { parseJsonObject, pruneEmptyDeep } from './helpers';

/*
Per-host override of an XHTTP split's download endpoint.

A host is another way in to the same inbound and only the upload half moves
with it, so an alternate entry point would otherwise download from whatever
address the inbound was built with. Leaving every field empty inherits the
inbound's endpoint; filling the address replaces it for this host alone.

Stored as a JSON string in the host's download_settings column, the same way
muxParams, sockoptParams and finalMask are.
*/

interface DownloadValues {
  address?: string;
  port?: number;
  security?: string;
  tlsSettings?: { serverName?: string; fingerprint?: string };
  realitySettings?: { serverName?: string; publicKey?: string; shortId?: string; fingerprint?: string };
}

export default function HostDownloadForm({
  value = '',
  onChange,
}: {
  value?: string;
  onChange?: (next: string) => void;
}) {
  const { t } = useTranslation();
  const [form] = Form.useForm();
  const [initial] = useState(() => parseJsonObject(value) as DownloadValues);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const download = Form.useWatch('download', form) as DownloadValues | undefined;
  const security = download?.security;

  useEffect(() => {
    if (download === undefined) return;
    /* No address means no override: the host inherits the inbound's endpoint
       rather than emitting a half-filled one that resolves nowhere. */
    const pruned = download.address ? (pruneEmptyDeep(download) as Record<string, unknown> | undefined) : undefined;
    const next = pruned ? JSON.stringify(pruned) : '';
    if (next !== value) onChangeRef.current?.(next);
  }, [download, value]);

  return (
    <Form
      form={form}
      component={false}
      colon={false}
      labelCol={{ sm: { span: 8 } }}
      wrapperCol={{ sm: { span: 14 } }}
      labelWrap
      initialValues={{ download: initial }}
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={t('pages.hosts.fields.downloadSettingsHint')}
      />
      <Form.Item
        name={['download', 'address']}
        label={t('pages.inbounds.form.splitDownloadAddress')}
        tooltip={t('pages.hosts.fields.downloadSettingsAddressHint')}
      >
        <Input placeholder="download.example.com" allowClear />
      </Form.Item>
      <Form.Item name={['download', 'port']} label={t('pages.inbounds.form.splitDownloadPort')}>
        <InputNumber min={0} max={65535} style={{ width: '100%' }} />
      </Form.Item>
      <Form.Item name={['download', 'security']} label={t('pages.inbounds.form.splitDownloadSecurity')}>
        <Select
          allowClear
          options={[
            { value: 'none', label: 'none' },
            { value: 'tls', label: 'tls' },
            { value: 'reality', label: 'reality' },
          ]}
        />
      </Form.Item>
      {security === 'tls' && (
        <>
          <Form.Item
            name={['download', 'tlsSettings', 'serverName']}
            label={t('pages.inbounds.form.splitDownloadSni')}
          >
            <Input placeholder="download.example.com" allowClear />
          </Form.Item>
          <Form.Item
            name={['download', 'tlsSettings', 'fingerprint']}
            label={t('pages.inbounds.form.splitDownloadFingerprint')}
          >
            <Input placeholder="chrome" allowClear />
          </Form.Item>
        </>
      )}
      {security === 'reality' && (
        <>
          <Form.Item
            name={['download', 'realitySettings', 'serverName']}
            label={t('pages.inbounds.form.splitDownloadSni')}
          >
            <Input placeholder="download.example.com" allowClear />
          </Form.Item>
          <Form.Item
            name={['download', 'realitySettings', 'publicKey']}
            label={t('pages.inbounds.form.splitDownloadPublicKey')}
          >
            <Input allowClear />
          </Form.Item>
          <Form.Item
            name={['download', 'realitySettings', 'shortId']}
            label={t('pages.inbounds.form.splitDownloadShortId')}
          >
            <Input allowClear />
          </Form.Item>
          <Form.Item
            name={['download', 'realitySettings', 'fingerprint']}
            label={t('pages.inbounds.form.splitDownloadFingerprint')}
          >
            <Input placeholder="chrome" allowClear />
          </Form.Item>
        </>
      )}
    </Form>
  );
}
