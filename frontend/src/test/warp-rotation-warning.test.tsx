import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import WarpModal from '@/pages/xray/overrides/WarpModal';
import { HttpUtil, Msg } from '@/utils';

afterEach(() => vi.restoreAllMocks());

describe('WARP IP rotation result', () => {
  it.each([undefined, 'failed to re-apply WARP license: license rate limited'])(
    'keeps the rotated outbound and presents its actual result (%s)',
    async (warning) => {
      const data = { device_id: 'rotated-device', license_key: 'saved-paid-license', private_key: 'new-private-key' };
      const config = { config: {
        interface: { addresses: { v4: '172.16.0.9' } },
        peers: [{ public_key: 'new-public-key', endpoint: { host: 'warp.example:2408' } }],
      } };
      vi.spyOn(HttpUtil, 'post').mockImplementation(async (url) => {
        const obj = url.endsWith('/changeIp')
          ? JSON.stringify({ data, config, warning })
          : url.endsWith('/data') ? JSON.stringify({ device_id: 'old-device' }) : {};
        return new Msg(true, '', obj) as never;
      });
      const onResetOutbound = vi.fn();
      render(<WarpModal
        open
        templateSettings={{ outbounds: [{ tag: 'warp' }] }}
        onClose={vi.fn()}
        onAddOutbound={vi.fn()}
        onResetOutbound={onResetOutbound}
        onRemoveOutbound={vi.fn()}
      />);
      const change = await screen.findByRole('button', { name: /Change IP/ });
      await waitFor(() => expect(change.hasAttribute('disabled')).toBe(false));
      fireEvent.click(change);

      await screen.findByText('rotated-device');
      expect(screen.getByText('saved-paid-license')).toBeTruthy();
      expect(onResetOutbound).toHaveBeenCalledWith({
        index: 0,
        outbound: expect.objectContaining({ settings: expect.objectContaining({ secretKey: 'new-private-key' }) }),
      });
      const notice = await screen.findByText(warning ?? 'WARP IP changed successfully!');
      expect(notice.closest(warning ? '.ant-message-warning' : '.ant-message-success')).toBeTruthy();
      if (warning) expect(screen.queryByText('WARP IP changed successfully!')).toBeNull();
    },
  );
});
