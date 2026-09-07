// Design-sync only. The theme module touches document.body and localStorage at
// import time; an embedding page may have neither when the bundle evaluates.
try {
  window.localStorage.getItem('ds-probe');
} catch {
  const mem = new Map<string, string>();
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => (mem.has(k) ? (mem.get(k) as string) : null),
      setItem: (k: string, v: string) => {
        mem.set(k, String(v));
      },
      removeItem: (k: string) => {
        mem.delete(k);
      },
      clear: () => mem.clear(),
      key: (i: number) => [...mem.keys()][i] ?? null,
      get length() {
        return mem.size;
      },
    },
  });
}

if (!document.body) {
  document.documentElement.appendChild(document.createElement('body'));
}

// Mirrors .storybook/preview-head.html: useTheme.tsx reads these at module
// scope and defaults to dark, so previews must seed light like the reference.
if (localStorage.getItem('dark-mode') === null) localStorage.setItem('dark-mode', 'false');
if (localStorage.getItem('isUltraDarkThemeEnabled') === null) {
  localStorage.setItem('isUltraDarkThemeEnabled', 'false');
}
