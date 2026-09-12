import { useId, type ReactNode } from 'react';
import { Icon } from '@/components/ui/Icon';

export default function SettingsGroup({ title, description, children, collapsible = false }: { title: ReactNode; description?: ReactNode; children: ReactNode; collapsible?: boolean }) {
  const titleId = useId();
  const body = <div className="settings-group-body">{children}</div>;
  return <section className="p-card settings-group" aria-labelledby={titleId}>
    {collapsible ? <details>
      <summary className="settings-group-header settings-group-toggle">
        <h3 id={titleId}>{title}<Icon name="chevronDown" size={15} /></h3>
      </summary>
      {description && <p className="settings-group-description">{description}</p>}
      {body}
    </details> : <>
      <header className="settings-group-header">
        <h3 id={titleId}>{title}</h3>
        {description && <p>{description}</p>}
      </header>
      {body}
    </>}
  </section>;
}
