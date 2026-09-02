import { afterEach, describe, expect, it, vi } from 'vitest';
import { message as staticMessage } from 'antd';
import type { MessageInstance } from 'antd/es/message/interface';

import { HttpUtil, Msg } from '@/utils';
import { setMessageInstance } from '@/utils/messageBus';

/*
A save that is only pending a restart says so.

The add and update responses set the core's restart flag silently; the page
showed the success toast and nothing else, so the operator learned the change
was not live only when a client could not connect. The response now carries
restartPending beside nodePending, and the page turns it into a toast. N4.
*/
describe('pending-flag toasts', () => {
  afterEach(() => {
    setMessageInstance(staticMessage as unknown as MessageInstance);
  });

  function recorder() {
    const warning = vi.fn();
    setMessageInstance({ success: vi.fn(), error: vi.fn(), warning } as unknown as MessageInstance);
    return warning;
  }

  it('warns when the response says a restart is pending', () => {
    const warning = recorder();
    HttpUtil._handleMsg(new Msg(true, 'saved', { restartPending: true }));
    expect(warning).toHaveBeenCalledTimes(1);
  });

  it('stays quiet when nothing is pending', () => {
    const warning = recorder();
    HttpUtil._handleMsg(new Msg(true, 'saved', { id: 7 }));
    expect(warning).not.toHaveBeenCalled();
  });
});
