import type { LucideIcon } from "lucide-react";

type EmptyViewProps = {
  title: string;
  description: string;
  icon: LucideIcon;
};

export function EmptyView({ title, description, icon: Icon }: EmptyViewProps) {
  return (
    <section className="workspace simple-workspace">
      <div className="workspace-header">
        <div>
          <h1>{title}</h1>
          <p>{description}</p>
        </div>
      </div>
      <div className="simple-empty">
        <span className="empty-icon"><Icon size={23} /></span>
        <h2>Nenhum item</h2>
      </div>
    </section>
  );
}

