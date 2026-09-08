import type { CSSProperties, ReactElement, ReactNode } from 'react';
import { Button, Tooltip } from 'antd';

import { Icon, type IconName } from '@/components/ui/Icon';

export type PanelStatus = { key: string; label: string; color: string };

export function Kicker({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return <div className="p-kicker" style={style}>{children}</div>;
}

export function StatusDot({ color, pulse }: { color: string; pulse?: boolean }) {
  return <span className="p-dot" style={{ background: color, boxShadow: pulse ? `0 0 0 3px ${color}2e` : 'none' }} />;
}

export function StatusChip({ status, size = 'default', pulse }: {
  status: PanelStatus;
  size?: 'default' | 'small';
  pulse?: boolean;
}) {
  return (
    <span
      className="p-chip"
      style={{
        color: `color-mix(in srgb, ${status.color} 58%, var(--p-ink))`,
        borderColor: `color-mix(in srgb, ${status.color} 38%, transparent)`,
        background: `color-mix(in srgb, ${status.color} 10%, transparent)`,
        fontSize: size === 'small' ? 10 : 11,
      }}
    >
      {pulse
        ? <span className="p-live-dot" style={{ background: status.color }}><i style={{ background: status.color }} /></span>
        : <StatusDot color={status.color} />}
      {status.label}
    </span>
  );
}

/* One dot carries both facts: colour and pulse encode the lifecycle status, the
   travelling ring means connected right now. The words live in the tooltip. */
export function StatusPip({ status, online, note, size = 9 }: {
  status: PanelStatus;
  online?: boolean;
  note?: ReactNode;
  size?: number;
}) {
  const kind = status.key === 'ended' ? 'error'
    : status.key === 'depleting' || status.key === 'expiring' ? 'warn'
      : status.key === 'disabled' ? 'off' : 'ok';
  const title = (
    <span style={{ display: 'block', lineHeight: 1.5 }}>
      <b>{status.label}</b>{online ? ' · online now' : ''}
      {note ? <><br /><span style={{ opacity: 0.75 }}>{note}</span></> : null}
    </span>
  );
  return (
    <Tooltip title={title} trigger={['hover', 'click']} mouseEnterDelay={0.08}>
      <button
        type="button"
        className={`p-pip is-${kind}${online ? ' is-live' : ''}`}
        style={{ color: status.color, width: size + 8, height: size + 8 }}
        aria-label={status.label}
      >
        {online && <i className="p-pip-ring" />}
        <i className="p-pip-core" style={{ width: size, height: size }} />
      </button>
    </Tooltip>
  );
}

export function LiveDot({ tone = 'var(--p-success)', size = 7 }: { tone?: string; size?: number }) {
  return (
    <span className="p-live-dot" style={{ background: tone, width: size, height: size, flex: `0 0 ${size}px` }}>
      <i style={{ background: tone }} />
    </span>
  );
}

export function LiveBadge({ label = 'Live', tone = 'var(--p-success)', title }: {
  label?: ReactNode;
  tone?: string;
  title?: string;
}) {
  return (
    <span
      className="p-live"
      style={{
        color: `color-mix(in srgb, ${tone} 58%, var(--p-ink))`,
        borderColor: `color-mix(in srgb, ${tone} 34%, transparent)`,
        background: `color-mix(in srgb, ${tone} 11%, transparent)`,
      }}
      title={title}
    >
      <span className="p-live-dot" style={{ background: tone }}><i style={{ background: tone }} /></span>
      {label}
    </span>
  );
}

export function Meter({ pct, tone, height = 5, track }: { pct: number; tone?: string; height?: number; track?: string }) {
  return (
    <div className="p-meter" style={{ height, background: track || 'var(--p-fill)' }}>
      <i style={{ width: `${Math.max(pct > 0 ? 2 : 0, pct)}%`, background: tone }} />
    </div>
  );
}

export function PanelCard({ title, kicker, icon, extra, children, pad = 16, style, className = '' }: {
  title?: ReactNode;
  kicker?: ReactNode;
  icon?: IconName;
  extra?: ReactNode;
  children?: ReactNode;
  pad?: number;
  style?: CSSProperties;
  className?: string;
}) {
  return (
    <section className={`p-card ${className}`} style={style}>
      {(title || kicker || extra) && (
        <header className="p-card-h">
          <div className="p-card-t">
            {icon && <span className="p-card-i"><Icon name={icon} size={14} /></span>}
            {kicker ? <Kicker>{kicker}</Kicker> : <span className="p-card-title">{title}</span>}
          </div>
          {extra && <div className="p-card-x">{extra}</div>}
        </header>
      )}
      <div style={{ padding: pad }}>{children}</div>
    </section>
  );
}

export function Field({ label, value, sub, align, tone }: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  align?: CSSProperties['textAlign'];
  tone?: string;
}) {
  return (
    <div style={{ textAlign: align || 'left', minWidth: 0 }}>
      <Kicker>{label}</Kicker>
      <div className="p-val" style={{ color: tone }}>{value}</div>
      {sub && <div className="p-sub">{sub}</div>}
    </div>
  );
}

export function PageHeader({ title, sub, actions, tabs }: {
  title: ReactNode;
  sub?: ReactNode;
  actions?: ReactNode;
  tabs?: ReactNode;
}) {
  return (
    <div className="p-pagehead">
      <div className="p-pagehead-row">
        <div className="p-pagehead-t">
          <h1 className="p-h1">{title}</h1>
          {sub && <div className="p-sub" style={{ marginTop: 3 }}>{sub}</div>}
        </div>
        {actions && <div className="p-pagehead-a">{actions}</div>}
      </div>
      {tabs}
    </div>
  );
}

export function Toolbar({ left, right }: { left?: ReactNode; right?: ReactNode }) {
  return (
    <div className="p-toolbar">
      <div className="p-toolbar-g">{left}</div>
      <div className="p-toolbar-g">{right}</div>
    </div>
  );
}

export type ActionItem = { icon: IconName; title: string; danger?: boolean; onClick?: () => void };

export function ActionIcons({ items }: { items: ActionItem[] }) {
  return (
    <div className="p-acts">
      {items.map((it) => (
        <Tooltip key={it.title} title={it.title}>
          <Button
            type="text"
            size="small"
            danger={it.danger}
            onClick={it.onClick}
            aria-label={it.title}
            icon={<Icon name={it.icon} size={15} />}
          />
        </Tooltip>
      ))}
    </div>
  );
}

/* Ring with the reading inside it, so the centre is not dead space. */
export function Gauge({ value, max = 100, tone = 'var(--p-primary)', size = 58, thick = 5, unit = '%', muted, decimals = 1 }: {
  value: number;
  max?: number;
  tone?: string;
  size?: number;
  thick?: number;
  unit?: string;
  muted?: boolean;
  decimals?: number;
}) {
  const r = (size - thick) / 2;
  const cx = size / 2;
  const circ = 2 * Math.PI * r;
  const pct = muted ? 0 : Math.max(0, Math.min(1, value / max));
  return (
    <span className="p-gauge" style={{ width: size, height: size }}>
      <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }}>
        <circle cx={cx} cy={cx} r={r} fill="none" stroke="var(--p-fill)" strokeWidth={thick} />
        {!muted && (
          <circle
            cx={cx}
            cy={cx}
            r={r}
            fill="none"
            stroke={tone}
            strokeWidth={thick}
            strokeLinecap="round"
            strokeDasharray={`${circ * pct} ${circ}`}
          />
        )}
      </svg>
      <span className="p-gauge-v">
        {muted ? <span className="p-gauge-off">—</span> : (
          <>
            <span className="p-gauge-n">{Number(value).toFixed(decimals)}</span>
            <span className="p-gauge-u">{unit}</span>
          </>
        )}
      </span>
    </span>
  );
}

export function Ring({ value, max = 100, label, sub, tone = 'var(--p-primary)', size = 58, thick = 5, unit = '%' }: {
  value: number;
  max?: number;
  label: ReactNode;
  sub?: ReactNode;
  tone?: string;
  size?: number;
  thick?: number;
  unit?: string;
}) {
  const r = (size - thick) / 2;
  const cx = size / 2;
  const circ = 2 * Math.PI * r;
  const pct = Math.max(0, Math.min(1, value / max));
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 11, minWidth: 0 }}>
      <svg width={size} height={size} style={{ flex: '0 0 auto', transform: 'rotate(-90deg)' }}>
        <circle cx={cx} cy={cx} r={r} fill="none" stroke="var(--p-fill)" strokeWidth={thick} />
        <circle cx={cx} cy={cx} r={r} fill="none" stroke={tone} strokeWidth={thick} strokeLinecap="round" strokeDasharray={`${circ * pct} ${circ}`} />
      </svg>
      <div style={{ minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 2 }}>
          <span style={{ fontSize: 19, fontWeight: 650, letterSpacing: '-.01em', color: 'var(--p-text)' }}>{value}</span>
          <span style={{ fontSize: 11, fontWeight: 600, color: 'var(--p-text-2)' }}>{unit}</span>
        </div>
        <Kicker style={{ marginTop: 1 }}>{label}</Kicker>
        {sub && <div className="p-sub" style={{ marginTop: 2, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{sub}</div>}
      </div>
    </div>
  );
}

export function ShareRow({ label, tag, value, pct, tone, sub, capped = true, cap }: {
  label: ReactNode;
  tag?: ReactNode;
  value: ReactNode;
  pct: number;
  tone?: string;
  sub?: ReactNode;
  capped?: boolean;
  cap?: ReactNode;
}) {
  return (
    <div className="p-share">
      <div className="p-share-h">
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>{tag}<span className="p-share-l">{label}</span></div>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flex: '0 0 auto' }}>
          <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--p-text)' }}>{value}</span>
          <span className="p-sub" style={{ minWidth: 34, textAlign: 'right' }}>
            {capped ? (cap ? <>of {cap}</> : `${Math.round(pct)}%`) : 'no cap'}
          </span>
        </div>
      </div>
      <Meter pct={capped ? pct : 100} tone={capped ? tone : 'color-mix(in srgb,#9254de 45%,transparent)'} height={4} />
      {sub && <div className="p-sub" style={{ marginTop: 4 }}>{sub}</div>}
    </div>
  );
}

export function LeaderRow({ rank, name, meta, value, pct, tone, right }: {
  rank: ReactNode;
  name: ReactNode;
  meta?: ReactNode;
  value: ReactNode;
  pct: number;
  tone?: string;
  right?: ReactNode;
}) {
  return (
    <div className="p-leader">
      <span className="p-rank">{rank}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10 }}>
          <span className="p-name" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{name}</span>
          <span style={{ fontSize: 12.5, fontWeight: 600, color: 'var(--p-text)', flex: '0 0 auto' }}>{value}</span>
        </div>
        <div style={{ margin: '5px 0 4px' }}><Meter pct={pct} tone={tone} height={3} /></div>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}><span className="p-sub">{meta}</span>{right}</div>
      </div>
    </div>
  );
}

export function EventItem({ time, title, meta, tone, icon, last }: {
  time: ReactNode;
  title: ReactNode;
  meta?: ReactNode;
  tone: string;
  icon: IconName;
  last?: boolean;
}) {
  return (
    <div className="p-event">
      <div className="p-event-g">
        <span className="p-event-i" style={{ background: `color-mix(in srgb, ${tone} 16%, transparent)`, color: tone }}>
          <Icon name={icon} size={12} />
        </span>
        {!last && <span className="p-event-line" />}
      </div>
      <div style={{ paddingBottom: last ? 0 : 16, minWidth: 0 }}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'baseline' }}>
          <span style={{ fontSize: 12.5, color: 'var(--p-text)', fontWeight: 550 }}>{title}</span>
          <span className="p-sub" style={{ flex: '0 0 auto' }}>{time}</span>
        </div>
        {meta && <div className="p-sub" style={{ marginTop: 2 }}>{meta}</div>}
      </div>
    </div>
  );
}

export function Delta({ value, invert }: { value: number; invert?: boolean }): ReactElement {
  const up = value >= 0;
  const good = invert ? !up : up;
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 11, fontWeight: 650, color: `color-mix(in srgb, var(${good ? '--p-success' : '--p-warning'}) 58%, var(--p-ink))` }}>
      <Icon name={up ? 'up' : 'down'} size={11} />{Math.abs(value)}%
    </span>
  );
}
