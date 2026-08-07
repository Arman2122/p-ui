import { useTranslation } from 'react-i18next';
import { AutoComplete, Button, Input, InputNumber, Select, Switch, Typography } from 'antd';
import { useFormContext, useWatch } from 'react-hook-form';

import { HeaderMapEditor } from '@/components/form';
import { FormField } from '@/components/form/rhf';
import { XHTTP_SESSION_ID_TABLES, XMUX_FRESH_DEFAULTS } from '@/schemas/protocols/stream/xhttp';
import { validateSessionIDLength, validateSessionIDTable } from '@/lib/xray/xhttp-session-id';
import { int32RangeUpper } from '@/lib/xray/stream-wire-normalize';

function antdValidatorToRhf(fn: (rule: unknown, value: unknown) => Promise<void>) {
  return async (value: unknown): Promise<true | string> => {
    try {
      await fn(undefined, value);
      return true;
    } catch (e) {
      return (e as Error).message;
    }
  };
}

export default function XhttpForm() {
  const { t } = useTranslation();
  const { control, getValues, setValue } = useFormContext();
  const xhttpMode = useWatch({ control, name: 'streamSettings.xhttpSettings.mode' }) as string | undefined;
  const xhttpObfsMode = !!useWatch({ control, name: 'streamSettings.xhttpSettings.xPaddingObfsMode' });
  const xhttpSessionIDPlacement = useWatch({ control, name: 'streamSettings.xhttpSettings.sessionIDPlacement' }) as string | undefined;
  const xhttpSessionIDTable = useWatch({ control, name: 'streamSettings.xhttpSettings.sessionIDTable' });
  const xhttpSeqPlacement = useWatch({ control, name: 'streamSettings.xhttpSettings.seqPlacement' }) as string | undefined;
  const xhttpUplinkPlacement = useWatch({ control, name: 'streamSettings.xhttpSettings.uplinkDataPlacement' }) as string | undefined;
  const enableXmux = !!useWatch({ control, name: 'streamSettings.xhttpSettings.enableXmux' });
  const enableDownload = !!useWatch({ control, name: 'streamSettings.xhttpSettings.enableDownloadSettings' });
  const downloadSecurity = useWatch({ control, name: 'streamSettings.xhttpSettings.downloadSettings.security' }) as string | undefined;
  const inboundPort = useWatch({ control, name: 'port' }) as number | undefined;
  const downloadPort = useWatch({ control, name: 'streamSettings.xhttpSettings.downloadSettings.port' }) as number | undefined;

  /* Legitimate only when a second inbound listens on the download address at
     that port, which is rarer than the typo it usually is. */
  const downloadPortUnserved = typeof inboundPort === 'number' && inboundPort > 0
    && typeof downloadPort === 'number' && downloadPort > 0
    && downloadPort !== inboundPort;

  /* stream-one is the one mode xray-core refuses to start with alongside a
     download endpoint, so the toggle says why rather than failing on save. */
  const splitBlockedByMode = xhttpMode === 'stream-one';

  function onDownloadToggle(checked: boolean) {
    if (!checked) return;
    const existing = getValues('streamSettings.xhttpSettings.downloadSettings');
    if (existing && typeof existing === 'object' && Object.keys(existing).length > 0) return;
    /* Seeded from this inbound, not from a constant: the usual split is one
       listener reached by a second address, so a fixed 443 seeds a port that
       nothing serves and only the download direction fails. */
    const port = getValues('port');
    setValue('streamSettings.xhttpSettings.downloadSettings', {
      address: '',
      port: typeof port === 'number' && port > 0 ? port : 443,
      network: 'xhttp',
      security: (getValues('streamSettings.security') as string) || 'none',
    });
  }

  /* The common deployment is one server answering on a second address, where
     everything except the address matches the upload side. Copying removes the
     chance of a mismatch nobody notices until the download half stops working. */
  function copyFromUploadSide() {
    const stream = getValues('streamSettings') as Record<string, unknown> | undefined;
    const security = (stream?.security as string | undefined) ?? 'none';
    const tls = stream?.tlsSettings as Record<string, unknown> | undefined;
    const reality = stream?.realitySettings as Record<string, unknown> | undefined;
    const path = getValues('streamSettings.xhttpSettings.path') as string | undefined;
    const host = getValues('streamSettings.xhttpSettings.host') as string | undefined;
    const port = getValues('port') as number | undefined;

    /* The port is the half that has to be copied. A second IP on this server
       reaches the same listener, so a download port that is not this inbound's
       port has nothing behind it and only the download direction fails. */
    if (typeof port === 'number' && port > 0) {
      setValue('streamSettings.xhttpSettings.downloadSettings.port', port);
    }
    setValue('streamSettings.xhttpSettings.downloadSettings.security', security);
    if (security !== 'tls') {
      setValue('streamSettings.xhttpSettings.downloadSettings.tlsSettings', undefined);
    }
    if (security !== 'reality') {
      setValue('streamSettings.xhttpSettings.downloadSettings.realitySettings', undefined);
    }
    if (security === 'tls' && tls) {
      setValue('streamSettings.xhttpSettings.downloadSettings.tlsSettings', {
        serverName: (tls.serverName as string) ?? '',
        alpn: Array.isArray(tls.alpn) ? tls.alpn : [],
        fingerprint: (tls.fingerprint as string) ?? '',
      });
    }
    if (security === 'reality' && reality) {
      /* The client-facing half of REALITY lives under realitySettings.settings;
         only serverNames and shortIds are top-level. Reading the wrong level
         copies an empty public key, which is the silent mismatch this button
         exists to prevent. */
      const client = (reality.settings ?? {}) as Record<string, unknown>;
      setValue('streamSettings.xhttpSettings.downloadSettings.realitySettings', {
        serverName: (client.serverName as string) || (reality.serverNames as string[] | undefined)?.[0] || '',
        publicKey: (client.publicKey as string) ?? '',
        shortId: (reality.shortIds as string[] | undefined)?.[0] ?? '',
        spiderX: (client.spiderX as string) ?? '',
        fingerprint: (client.fingerprint as string) ?? 'chrome',
      });
    }
    setValue('streamSettings.xhttpSettings.downloadSettings.xhttpSettings', {
      path: path || '/',
      ...(host ? { host } : {}),
    });
  }

  function onXmuxToggle(checked: boolean) {
    if (!checked) return;
    const existing = getValues('streamSettings.xhttpSettings.xmux');
    const hasValues = existing && typeof existing === 'object' && Object.keys(existing).length > 0;
    if (hasValues) return;
    setValue('streamSettings.xhttpSettings.xmux', { ...XMUX_FRESH_DEFAULTS });
  }

  function onXmuxMaxConcurrencyChange(value: unknown) {
    if (int32RangeUpper(value) <= 0) return;
    if (int32RangeUpper(getValues('streamSettings.xhttpSettings.xmux.maxConnections')) > 0) {
      setValue('streamSettings.xhttpSettings.xmux.maxConnections', 0);
    }
  }

  function onXmuxMaxConnectionsChange(value: unknown) {
    if (int32RangeUpper(value) <= 0) return;
    if (int32RangeUpper(getValues('streamSettings.xhttpSettings.xmux.maxConcurrency')) > 0) {
      setValue('streamSettings.xhttpSettings.xmux.maxConcurrency', '');
    }
  }

  return (
    <>
      <FormField name={['streamSettings', 'xhttpSettings', 'host']} label={t('host')}>
        <Input />
      </FormField>
      <FormField name={['streamSettings', 'xhttpSettings', 'path']} label={t('path')}>
        <Input />
      </FormField>
      <FormField name={['streamSettings', 'xhttpSettings', 'mode']} label={t('pages.inbounds.info.mode')}>
        <Select
          style={{ width: '50%' }}
          options={(['auto', 'packet-up', 'stream-up', 'stream-one'] as const).map((m) => ({
            value: m,
            label: m,
          }))}
        />
      </FormField>
      {(xhttpMode === 'packet-up' || xhttpMode === 'auto') && (
        <>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'scMaxEachPostBytes']}
            label={t('pages.inbounds.form.maxUploadSize')}
          >
            <Input />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'scMaxBufferedPosts']}
            label={t('pages.inbounds.form.maxBufferedUpload')}
          >
            <InputNumber />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'scMinPostsIntervalMs']}
            label={t('pages.xray.outboundForm.minUploadInterval')}
          >
            <Input placeholder="e.g. 50-150" />
          </FormField>
        </>
      )}
      {xhttpMode === 'stream-up' && (
        <>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'scMaxBufferedPosts']}
            label={t('pages.inbounds.form.maxBufferedUpload')}
          >
            <InputNumber />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'scStreamUpServerSecs']}
            label={t('pages.inbounds.form.streamUpServer')}
          >
            <Input />
          </FormField>
        </>
      )}
      <FormField
        name={['streamSettings', 'xhttpSettings', 'serverMaxHeaderBytes']}
        label={t('pages.inbounds.form.serverMaxHeaderBytes')}
      >
        <InputNumber min={0} placeholder="0 (default)" />
      </FormField>
      <FormField
        name={['streamSettings', 'xhttpSettings', 'xPaddingBytes']}
        label={t('pages.inbounds.form.paddingBytes')}
      >
        <Input />
      </FormField>
      <FormField
        name={['streamSettings', 'xhttpSettings', 'headers']}
        label={t('pages.inbounds.form.headers')}
      >
        <HeaderMapEditor mode="v1" />
      </FormField>
      <FormField
        name={['streamSettings', 'xhttpSettings', 'uplinkHTTPMethod']}
        label={t('pages.inbounds.form.uplinkHttpMethod')}
      >
        <Select
          options={[
            { value: '', label: 'Default (POST)' },
            { value: 'POST', label: 'POST' },
            { value: 'PUT', label: 'PUT' },
            {
              value: 'GET',
              label: 'GET (packet-up only)',
              disabled: xhttpMode !== 'packet-up',
            },
          ]}
        />
      </FormField>
      <FormField
        name={['streamSettings', 'xhttpSettings', 'xPaddingObfsMode']}
        label={t('pages.inbounds.form.paddingObfsMode')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      {xhttpObfsMode && (
        <>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'xPaddingKey']}
            label={t('pages.inbounds.form.paddingKey')}
          >
            <Input placeholder="x_padding" />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'xPaddingHeader']}
            label={t('pages.inbounds.form.paddingHeader')}
          >
            <Input placeholder="X-Padding" />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'xPaddingPlacement']}
            label={t('pages.inbounds.form.paddingPlacement')}
          >
            <Select
              options={[
                { value: '', label: 'Default (queryInHeader)' },
                { value: 'queryInHeader', label: 'queryInHeader' },
                { value: 'header', label: 'header' },
                { value: 'cookie', label: 'cookie' },
                { value: 'query', label: 'query' },
              ]}
            />
          </FormField>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'xPaddingMethod']}
            label={t('pages.inbounds.form.paddingMethod')}
          >
            <Select
              options={[
                { value: '', label: 'Default (repeat-x)' },
                { value: 'repeat-x', label: 'repeat-x' },
                { value: 'tokenish', label: 'tokenish' },
              ]}
            />
          </FormField>
        </>
      )}
      <FormField
        name={['streamSettings', 'xhttpSettings', 'sessionIDPlacement']}
        label={t('pages.inbounds.form.sessionPlacement')}
      >
        <Select
          options={[
            { value: '', label: 'Default (path)' },
            { value: 'path', label: 'path' },
            { value: 'header', label: 'header' },
            { value: 'cookie', label: 'cookie' },
            { value: 'query', label: 'query' },
          ]}
        />
      </FormField>
      {xhttpSessionIDPlacement && xhttpSessionIDPlacement !== 'path' && (
        <FormField
          name={['streamSettings', 'xhttpSettings', 'sessionIDKey']}
          label={t('pages.inbounds.form.sessionKey')}
        >
          <Input placeholder="x_session" />
        </FormField>
      )}
      <FormField
        name={['streamSettings', 'xhttpSettings', 'sessionIDTable']}
        label={t('pages.inbounds.form.sessionIDTable')}
        tooltip={t('pages.inbounds.form.sessionIDTableHint')}
        rules={{ validate: antdValidatorToRhf(validateSessionIDTable) }}
      >
        <AutoComplete
          allowClear
          options={XHTTP_SESSION_ID_TABLES.map((v) => ({ value: v }))}
          placeholder="Base62"
        />
      </FormField>
      {!!xhttpSessionIDTable && (
        <FormField
          name={['streamSettings', 'xhttpSettings', 'sessionIDLength']}
          label={t('pages.inbounds.form.sessionIDLength')}
          tooltip={t('pages.inbounds.form.sessionIDLengthHint')}
          rules={{ validate: antdValidatorToRhf(validateSessionIDLength) }}
        >
          <Input placeholder="8-16" />
        </FormField>
      )}
      <FormField
        name={['streamSettings', 'xhttpSettings', 'seqPlacement']}
        label={t('pages.inbounds.form.sequencePlacement')}
      >
        <Select
          options={[
            { value: '', label: 'Default (path)' },
            { value: 'path', label: 'path' },
            { value: 'header', label: 'header' },
            { value: 'cookie', label: 'cookie' },
            { value: 'query', label: 'query' },
          ]}
        />
      </FormField>
      {xhttpSeqPlacement && xhttpSeqPlacement !== 'path' && (
        <FormField
          name={['streamSettings', 'xhttpSettings', 'seqKey']}
          label={t('pages.inbounds.form.sequenceKey')}
        >
          <Input placeholder="x_seq" />
        </FormField>
      )}
      {xhttpMode === 'packet-up' && (
        <>
          <FormField
            name={['streamSettings', 'xhttpSettings', 'uplinkDataPlacement']}
            label={t('pages.inbounds.form.uplinkDataPlacement')}
          >
            <Select
              options={[
                { value: '', label: 'Default (auto)' },
                { value: 'auto', label: 'auto' },
                { value: 'body', label: 'body' },
                { value: 'header', label: 'header' },
                { value: 'cookie', label: 'cookie' },
              ]}
            />
          </FormField>
          {xhttpUplinkPlacement && xhttpUplinkPlacement !== 'body' && (
            <FormField
              name={['streamSettings', 'xhttpSettings', 'uplinkDataKey']}
              label={t('pages.inbounds.form.uplinkDataKey')}
            >
              <Input placeholder="x_data" />
            </FormField>
          )}
        </>
      )}
      <FormField
        name={['streamSettings', 'xhttpSettings', 'noSSEHeader']}
        label={t('pages.inbounds.form.noSseHeader')}
        valueProp="checked"
      >
        <Switch />
      </FormField>
      {/* Split download: clients upload here and download from a
          different address, which is how one server with two IPs
          keeps the two directions off a single path. Only the
          upload half is a listener, so this travels to clients in
          their link and is stripped from the server config. */}
      <FormField
        label={t('pages.inbounds.form.splitDownload')}
        tooltip={splitBlockedByMode
          ? t('pages.inbounds.form.splitDownloadNotInStreamOne')
          : t('pages.inbounds.form.splitDownloadHint')}
        name={['streamSettings', 'xhttpSettings', 'enableDownloadSettings']}
        valueProp="checked"
        onAfterChange={(v) => onDownloadToggle(v as boolean)}
      >
        <Switch disabled={splitBlockedByMode} />
      </FormField>
      {enableDownload && !splitBlockedByMode && (
        <>
          <FormField
            label={t('pages.inbounds.form.splitDownloadAddress')}
            tooltip={t('pages.inbounds.form.splitDownloadAddressHint')}
            name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'address']}
            extra={(
              <Button size="small" type="link" style={{ paddingInline: 0 }} onClick={copyFromUploadSide}>
                {t('pages.inbounds.form.splitDownloadCopyUpload')}
              </Button>
            )}
          >
            <Input placeholder="download.example.com" />
          </FormField>
          <FormField
            label={t('pages.inbounds.form.splitDownloadPort')}
            tooltip={t('pages.inbounds.form.splitDownloadPortHint')}
            name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'port']}
            extra={downloadPortUnserved
              ? (
                <Typography.Text type="warning">
                  {t('pages.inbounds.form.splitDownloadPortMismatch', { port: inboundPort })}
                </Typography.Text>
              )
              : undefined}
          >
            <InputNumber min={0} max={65535} style={{ width: '100%' }} />
          </FormField>
          <FormField
            label={t('pages.inbounds.form.splitDownloadSecurity')}
            tooltip={t('pages.inbounds.form.splitDownloadSecurityHint')}
            name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'security']}
          >
            <Select
              options={[
                { value: 'none', label: 'none' },
                { value: 'tls', label: 'tls' },
                { value: 'reality', label: 'reality' },
              ]}
            />
          </FormField>
          {downloadSecurity === 'tls' && (
            <>
              <FormField
                label={t('pages.inbounds.form.splitDownloadSni')}
                tooltip={t('pages.inbounds.form.splitDownloadSniHint')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'tlsSettings', 'serverName']}
              >
                <Input placeholder="download.example.com" />
              </FormField>
              <FormField
                label={t('pages.inbounds.form.splitDownloadAlpn')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'tlsSettings', 'alpn']}
              >
                <Select
                  mode="multiple"
                  allowClear
                  options={[
                    { value: 'h3', label: 'h3' },
                    { value: 'h2', label: 'h2' },
                    { value: 'http/1.1', label: 'http/1.1' },
                  ]}
                />
              </FormField>
              <FormField
                label={t('pages.inbounds.form.splitDownloadFingerprint')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'tlsSettings', 'fingerprint']}
              >
                <Input placeholder="chrome" />
              </FormField>
            </>
          )}
          {downloadSecurity === 'reality' && (
            <>
              <FormField
                label={t('pages.inbounds.form.splitDownloadSni')}
                tooltip={t('pages.inbounds.form.splitDownloadSniHint')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'realitySettings', 'serverName']}
              >
                <Input placeholder="download.example.com" />
              </FormField>
              <FormField
                label={t('pages.inbounds.form.splitDownloadPublicKey')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'realitySettings', 'publicKey']}
              >
                <Input />
              </FormField>
              <FormField
                label={t('pages.inbounds.form.splitDownloadShortId')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'realitySettings', 'shortId']}
              >
                <Input />
              </FormField>
              <FormField
                label={t('pages.inbounds.form.splitDownloadFingerprint')}
                name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'realitySettings', 'fingerprint']}
              >
                <Input placeholder="chrome" />
              </FormField>
            </>
          )}
          <FormField
            label={t('pages.inbounds.form.splitDownloadPath')}
            tooltip={t('pages.inbounds.form.splitDownloadPathHint')}
            name={['streamSettings', 'xhttpSettings', 'downloadSettings', 'xhttpSettings', 'path']}
          >
            <Input placeholder="/" />
          </FormField>
        </>
      )}
      {/* XMUX is the connection-multiplexing layer
          xHTTP uses to fan out parallel requests over
          a small pool of upstream connections. UI-only
          toggle (enableXmux) hides the 6 nested knobs
          when off. */}
      <FormField
        label="XMUX"
        name={['streamSettings', 'xhttpSettings', 'enableXmux']}
        valueProp="checked"
        onAfterChange={(v) => onXmuxToggle(v as boolean)}
      >
        <Switch />
      </FormField>
      {enableXmux && (
        <>
          <FormField
            label={t('pages.xray.outboundForm.maxConcurrency')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'maxConcurrency']}
            onAfterChange={onXmuxMaxConcurrencyChange}
          >
            <Input placeholder="16-32" />
          </FormField>
          <FormField
            label={t('pages.xray.outboundForm.maxConnections')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'maxConnections']}
            onAfterChange={onXmuxMaxConnectionsChange}
          >
            <Input placeholder="0" />
          </FormField>
          <FormField
            label={t('pages.xray.outboundForm.maxReuseTimes')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'cMaxReuseTimes']}
          >
            <Input />
          </FormField>
          <FormField
            label={t('pages.xray.outboundForm.maxRequestTimes')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'hMaxRequestTimes']}
          >
            <Input placeholder="600-900" />
          </FormField>
          <FormField
            label={t('pages.xray.outboundForm.maxReusableSecs')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'hMaxReusableSecs']}
          >
            <Input placeholder="1800-3000" />
          </FormField>
          <FormField
            label={t('pages.xray.outboundForm.keepAlivePeriod')}
            name={['streamSettings', 'xhttpSettings', 'xmux', 'hKeepAlivePeriod']}
          >
            <InputNumber min={0} style={{ width: '100%' }} />
          </FormField>
        </>
      )}
    </>
  );
}
