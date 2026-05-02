import { Building2, Users, Briefcase, Globe } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

// Demo tenants for the RAG demo - maps to actual database tenants
const DEMO_TENANTS = [
  {
    id: "VeloDB Sample",
    corpusId: "velodb_docs",
    name: "VeloDB Documentation",
    description: "VeloDB docs with multimodal content",
    icon: Building2,
    color: "text-blue-500",
  },
  {
    id: "default",
    corpusId: "default",
    name: "Demo Tenant",
    description: "Default demo environment",
    icon: Globe,
    color: "text-cyan-500",
  },
  {
    id: "acme-corp",
    corpusId: "acme_docs",
    name: "Acme Corporation",
    description: "Enterprise customer demo",
    icon: Briefcase,
    color: "text-purple-500",
  },
];

interface TenantSelectorProps {
  value: string;
  onChange: (tenantId: string) => void;
  className?: string;
  disabled?: boolean;
}

export function TenantSelector({
  value,
  onChange,
  className = "",
  disabled = false,
}: TenantSelectorProps) {
  const selectedTenant = DEMO_TENANTS.find((t) => t.id === value) || DEMO_TENANTS[0];
  const Icon = selectedTenant.icon;

  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger
        className={`w-[220px] ${className}`}
        data-testid="tenant-selector"
      >
        <div className="flex items-center gap-2">
          <Icon className={`w-4 h-4 ${selectedTenant.color}`} />
          <SelectValue placeholder="Select tenant">
            {selectedTenant.name}
          </SelectValue>
        </div>
      </SelectTrigger>
      <SelectContent>
        {DEMO_TENANTS.map((tenant) => {
          const TenantIcon = tenant.icon;
          return (
            <SelectItem
              key={tenant.id}
              value={tenant.id}
              data-testid={`tenant-option-${tenant.id}`}
            >
              <div className="flex items-center gap-2">
                <TenantIcon className={`w-4 h-4 ${tenant.color}`} />
                <div className="flex flex-col">
                  <span className="font-medium">{tenant.name}</span>
                  <span className="text-xs text-muted-foreground">
                    {tenant.description}
                  </span>
                </div>
              </div>
            </SelectItem>
          );
        })}
      </SelectContent>
    </Select>
  );
}

export { DEMO_TENANTS };
export default TenantSelector;
