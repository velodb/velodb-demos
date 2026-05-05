import { NavLink } from "./NavLink";
import { Network, GitBranch, Hexagon, Settings, Bot } from "lucide-react";
import { cn } from "@/lib/utils";

const navigationItems = [
  { name: "Customer Facing", href: "/", icon: Network },
  { name: "Observability", href: "/observability", icon: GitBranch },
  { name: "GenAI", href: "/rag", icon: Bot },
  { name: "Lakehouse (TODO)", href: "/lakehouse", icon: Hexagon },
  { name: "Settings", href: "/settings", icon: Settings },
];

export const Sidebar = () => {
  return (
    <aside className="w-64 bg-sidebar border-r border-sidebar-border h-screen flex flex-col">
      <div className="p-6 border-b border-sidebar-border">
        <div className="flex items-center gap-2">
          <div className="w-8 h-8 bg-primary rounded flex items-center justify-center">
            <span className="text-primary-foreground font-bold text-sm">V</span>
          </div>
          <span className="font-semibold text-foreground text-lg">VeloDB</span>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <div className="space-y-1">
          <div className="px-3 py-2 text-xs font-medium text-muted-foreground">
            Use Cases
          </div>
          {navigationItems.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.name}
                to={item.href}
                className="flex items-center gap-3 px-3 py-2 rounded-md text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors"
                activeClassName="bg-sidebar-accent text-sidebar-accent-foreground font-medium"
              >
                <Icon className="w-5 h-5" />
                <span>{item.name}</span>
              </NavLink>
            );
          })}
        </div>

      </div>
    </aside>
  );
};
