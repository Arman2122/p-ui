import i18next from 'i18next';

/* A core arrives as its id ("xray"), which is also the stem of its display key.
   Resolving it here keeps the server from having to guess the reader's locale. */
function humanize(name: string, value: string): string {
  if (name !== 'core' || !value) return value;
  const key = `cores.${value}.title`;
  const translated = i18next.t(key);
  return translated === key ? value : translated;
}

/*
Renders a server-sent event. The wire carries an i18n key plus parameters rather
than a sentence, so the same event reads correctly in either locale and a
language switch re-renders history that is already in the feed.
*/
export function translateEvent(key: string, params?: Record<string, string>): string {
  const resolved: Record<string, string> = {};
  for (const [name, value] of Object.entries(params ?? {})) resolved[name] = humanize(name, value);
  const text = i18next.t(key, resolved);
  return text === key ? '' : text;
}
