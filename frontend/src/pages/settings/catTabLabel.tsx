import type { ReactNode } from 'react';

export function catTabLabel(icon: ReactNode, text: ReactNode, compact: boolean): ReactNode {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
      {!compact && icon}
      <span>{text}</span>
    </span>
  );
}
