import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import XrayPage from '@/pages/xray/XrayPage';

vi.mock('@/pages/xray/CoreConfigsPage', () => ({
  default: () => <h1>Core configuration</h1>,
  downloadConfig: vi.fn(),
}));

function open(path: string) {
  render(<MemoryRouter initialEntries={[path]}><Routes>
    <Route path="/xray" element={<XrayPage />} />
    <Route path="/routing" element={<h1>Network routing</h1>} />
    <Route path="/outbound" element={<h1>Network outbounds</h1>} />
  </Routes></MemoryRouter>);
}

describe('core configuration bookmarks', () => {
  it.each([
    ['/xray#routing', 'Network routing'],
    ['/xray?core=xray#outbound', 'Network outbounds'],
  ])('opens the dedicated network page for %s', async (path, title) => {
    open(path);
    expect(await screen.findByRole('heading', { name: title })).toBeDefined();
    expect(screen.queryByRole('heading', { name: 'Core configuration' })).toBeNull();
  });

  it('keeps a selected managed core when an old hash remains', () => {
    open('/xray?core=mtproto#routing');
    expect(screen.getByRole('heading', { name: 'Core configuration' })).toBeDefined();
  });
});
