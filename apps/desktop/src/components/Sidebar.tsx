import type { ViewId } from "../types";
import { primaryNavigation, secondaryNavigation, type NavigationItem } from "../app/navigation";

type SidebarProps = {
  activeView: ViewId;
  onChange: (view: ViewId) => void;
  searchRunning?: boolean;
};

function NavigationButton({
  item,
  active,
  onChange,
  showPulse,
}: {
  item: NavigationItem;
  active: boolean;
  onChange: (view: ViewId) => void;
  showPulse?: boolean;
}) {
  const Icon = item.icon;
  return (
    <button
      className={`nav-button ${active ? "is-active" : ""} ${showPulse ? "is-busy" : ""}`}
      type="button"
      aria-label={item.label}
      aria-current={active ? "page" : undefined}
      aria-busy={showPulse || undefined}
      title={showPulse ? `${item.label} (buscando...)` : item.label}
      onClick={() => onChange(item.id)}
    >
      <Icon size={20} strokeWidth={active ? 2 : 1.7} />
    </button>
  );
}

export function Sidebar({ activeView, onChange, searchRunning }: SidebarProps) {
  return (
    <nav className="sidebar" aria-label="Navegação principal">
      <div className="nav-group">
        {primaryNavigation.map((item) => (
          <NavigationButton
            key={item.id}
            item={item}
            active={activeView === item.id}
            onChange={onChange}
            showPulse={item.id === "search" && searchRunning}
          />
        ))}
      </div>
      <div className="nav-group nav-group-bottom">
        {secondaryNavigation.map((item) => (
          <NavigationButton key={item.id} item={item} active={activeView === item.id} onChange={onChange} />
        ))}
      </div>
    </nav>
  );
}
