import { afterEach, vi } from 'vitest';
import { cleanup } from '@testing-library/react';
import i18next from 'i18next';
import { initReactI18next } from 'react-i18next';

import enUS from '../../../internal/web/translation/en-US.json';

vi.mock('persian-calendar-suite', () => ({
  PersianDateTimePicker: () => null,
}));

if (typeof globalThis.localStorage === 'undefined') {
  const store = new Map<string, string>();
  const storage = {
    getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => { store.clear(); },
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() { return store.size; },
  } as Storage;
  Object.defineProperty(globalThis, 'localStorage', { value: storage, configurable: true });
  Object.defineProperty(globalThis, 'sessionStorage', { value: storage, configurable: true });
}

if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    lng: 'en-US',
    fallbackLng: 'en-US',
    resources: { 'en-US': { translation: enUS } },
    interpolation: { escapeValue: false, prefix: '{', suffix: '}' },
    returnNull: false,
  });
}

afterEach(async () => {
  cleanup();
  document.body.innerHTML = '';
  /*
   * A file that calls vi.restoreAllMocks() restores these stubs too, and every
   * later test in it then dials the real client: a relative URL fails to parse
   * in Node, the failure raises an antd toast, and the toast's close timer
   * fires after jsdom is torn down. Put the stubs back after every test.
   */
  stubHttp();
  /*
   * React 19 defers passive-effect flushes onto a macrotask (setImmediate),
   * whose callback reads `window.event`. If one is still queued when vitest
   * tears down the jsdom environment, it fires after `window` is gone and
   * throws "window is not defined". Drain here so any pending callback runs
   * while `window` still exists.
   *
   * BOTH queues, and both on every pass. Three setTimeout(0) ticks was the
   * first version of this and CI caught it out: the scheduler's callback is a
   * setImmediate (`Immediate.performWorkUntilDeadline` is in that stack), and
   * timers and immediates are different phases of the node loop — a timer tick
   * does not guarantee an immediate queued behind it has run. On a loaded
   * runner one survived all three passes and fired into a torn-down
   * environment, failing a suite in which every one of 1525 tests had passed.
   */
  for (let i = 0; i < 4; i += 1) {
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
});

import { HttpUtil } from '@/utils';

// stubHttp keeps every test off the network; re-applied after each test because
// vi.restoreAllMocks() in a file would otherwise strip it for the tests after.
function stubHttp() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(HttpUtil, 'post').mockResolvedValue({ success: true, obj: {} } as any);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(HttpUtil, 'get').mockResolvedValue({ success: true, obj: {} } as any);
}
stubHttp();

/* CodeMirror's selection layer measures text through Range.getClientRects, which
   jsdom leaves undefined; without it every form that mounts an editor throws
   mid-render. Zero-sized geometry is enough: nothing here asserts on layout. */
if (typeof Range !== 'undefined' && typeof Range.prototype.getClientRects !== 'function') {
  const emptyRect = (): DOMRect => ({ x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON: () => ({}) }) as DOMRect;
  Range.prototype.getClientRects = () => ({ length: 0, item: () => null, [Symbol.iterator]: [][Symbol.iterator] }) as unknown as DOMRectList;
  Range.prototype.getBoundingClientRect = emptyRect;
}
