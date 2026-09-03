import { describe, it, expect, vi } from 'vitest';

import { HttpUtil } from '@/utils';

/*
The components setup stubs HttpUtil so no test ever reaches the network. A test
file that calls vi.restoreAllMocks() in its afterEach restored those stubs too,
so every later test in the file dialled the real client: a relative URL fails to
parse in Node, the failure raises an antd toast, and the toast's close timer
fired after vitest had torn down jsdom — "window is not defined", from a file in
which every test had passed. The setup now puts the stubs back after each test.
*/
describe('the HTTP stubs outlive a test that restores its mocks', () => {
  it('a test restores every mock, as several files do', () => {
    expect(vi.isMockFunction(HttpUtil.post)).toBe(true);
    vi.restoreAllMocks();
    expect(vi.isMockFunction(HttpUtil.post)).toBe(false);
  });

  it('the next test still has the stubs, so nothing dials the network', async () => {
    expect(vi.isMockFunction(HttpUtil.post)).toBe(true);
    expect(vi.isMockFunction(HttpUtil.get)).toBe(true);
    const msg = await HttpUtil.post('/panel/api/anything');
    expect(msg.success).toBe(true);
  });
});
