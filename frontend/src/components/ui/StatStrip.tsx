import type { ReactNode } from 'react';

import { Kicker, StatusDot } from '@/components/ui/panel';

export interface StatStripItem {
  key: string;
  label: ReactNode;
  value: ReactNode;
  /** A dot before the number; use it for the buckets that mean something is wrong. */
  color?: string;
  pulse?: boolean;
}

/*
The row of counts above a table. When onPick is given each cell is also the
filter for that bucket, so the summary and the filter are one control rather
than a number next to a separate dropdown that can disagree with it.
*/
export default function StatStrip({ items, active, onPick }: {
  items: StatStripItem[];
  active?: string;
  onPick?: (key: string) => void;
}) {
  return (
    <section className="p-card p-strip" data-n={items.length}>
      {items.map((item) => {
        const body = (
          <>
            <Kicker>{item.label}</Kicker>
            <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
              {item.color && <StatusDot color={item.color} pulse={item.pulse} />}
              <span style={{ fontSize: 19, fontWeight: 650, color: 'var(--p-text)', letterSpacing: '-.01em' }}>
                {item.value}
              </span>
            </div>
          </>
        );
        if (!onPick) return <div key={item.key} className="p-strip-c">{body}</div>;
        return (
          <button
            type="button"
            key={item.key}
            className={`p-strip-c is-clickable${active === item.key ? ' is-on' : ''}`}
            aria-pressed={active === item.key}
            onClick={() => onPick(item.key)}
          >
            {body}
          </button>
        );
      })}
    </section>
  );
}
