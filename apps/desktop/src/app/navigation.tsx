import {
  Bookmark,
  BriefcaseBusiness,
  FileText,
  History,
  Search,
  Settings,
  TerminalSquare,
  type LucideIcon,
} from "lucide-react";
import type { ViewId } from "../types";

export type NavigationItem = {
  id: ViewId;
  label: string;
  icon: LucideIcon;
};

export const primaryNavigation: NavigationItem[] = [
  { id: "search", label: "Buscar vagas", icon: Search },
  { id: "saved", label: "Vagas salvas", icon: Bookmark },
  { id: "applications", label: "Candidaturas", icon: BriefcaseBusiness },
  { id: "history", label: "Historico", icon: History },
  // Label in English by design: the Resume Studio module is written in
  // English (the product is moving to a 100% English UI); the rest of
  // the app's navigation stays PT for now.
  { id: "resume", label: "Resume Studio", icon: FileText },
];

export const secondaryNavigation: NavigationItem[] = [
  { id: "logs", label: "Logs", icon: TerminalSquare },
  { id: "settings", label: "Configuracoes", icon: Settings },
];

export const viewLabels: Record<ViewId, string> = {
  search: "Vagas",
  saved: "Vagas salvas",
  applications: "Candidaturas",
  history: "Historico",
  logs: "Logs",
  settings: "Configuracoes",
  resume: "Resume Studio",
};
