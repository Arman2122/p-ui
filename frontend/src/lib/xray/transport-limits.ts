/*
Two transport values the core refuses outright, checked where the operator types
them rather than after the save bounces back as a raw error.

Both are xray's own rules, not the panel's, and both take down more than the
field they are in: xray aborts the WHOLE config on either, so every inbound of
every protocol stops answering at the next restart.
*/

/**
 * mKCP's send buffer is `maxSendingWindow / mtu`, so a window smaller than the
 * MTU computes to zero buffers and the core refuses the config. The form
 * offered a floor of 0 against an MTU floor of 576. F324.
 */
export function kcpSendingWindowFits(maxSendingWindow: unknown, mtu: unknown): boolean {
  const window = typeof maxSendingWindow === 'number' ? maxSendingWindow : NaN;
  const unit = typeof mtu === 'number' ? mtu : NaN;
  if (Number.isNaN(window) || Number.isNaN(unit) || unit <= 0) return true;
  return window >= unit;
}

/**
 * httpupgrade and xhttp carry the host in their own `host` field and xray
 * refuses a `host` key in the header map, whatever its case. Websocket only
 * warns and migrates it, which is why the shared editor cannot carry this rule
 * itself. F325.
 */
export function headerMapNamesHost(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false;
  return Object.keys(value as Record<string, unknown>)
    .some((name) => name.trim().toLowerCase() === 'host');
}
