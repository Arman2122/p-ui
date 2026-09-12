export function formatApiJson(raw: string): string {
  const pieces: string[] = [];
  let depth = 0, quoted = false, escaped = false, previous = '';
  const newline = () => pieces.push(`\n${'  '.repeat(Math.min(depth, 20))}`);
  for (let index = 0; index < raw.length; index += 1) {
    const char = raw[index];
    if (quoted) {
      pieces.push(char);
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') quoted = false;
      continue;
    }
    if (/\s/.test(char)) continue;
    if (char === '"') { quoted = true; pieces.push(char); }
    else if (char === '{' || char === '[') {
      pieces.push(char); depth += 1;
      let next = index + 1;
      while (next < raw.length && /\s/.test(raw[next])) next += 1;
      if (raw[next] !== '}' && raw[next] !== ']') newline();
    } else if (char === '}' || char === ']') {
      depth = Math.max(0, depth - 1);
      if (previous !== '{' && previous !== '[') newline();
      pieces.push(char);
    } else if (char === ',') { pieces.push(char); newline(); }
    else if (char === ':') pieces.push(': ');
    else pieces.push(char);
    previous = char;
  }
  return pieces.join('');
}
