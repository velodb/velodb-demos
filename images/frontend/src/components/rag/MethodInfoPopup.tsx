import { HelpCircle, CheckCircle2, AlertCircle, ArrowRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

export interface MethodInfo {
  title: string;
  icon: React.ElementType;
  color: string;
  bgColor: string;
  borderColor: string;
  description: string;
  howItWorks: string[];
  bestFor: string[];
  weakFor: string[];
}

interface MethodInfoPopupProps {
  methodInfo: MethodInfo;
  method: string;
}

const MethodInfoPopup = ({ methodInfo, method }: MethodInfoPopupProps) => {
  const Icon = methodInfo.icon;

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-7 w-7 p-0 hover:bg-white/50"
          data-testid={`info-button-${method}`}
          title="How it works"
        >
          <HelpCircle className="w-4 h-4 text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-80 p-0"
        align="end"
        data-testid={`info-popup-${method}`}
      >
        {/* Header */}
        <div
          className={cn(
            "flex items-center gap-2 p-3 border-b",
            methodInfo.bgColor
          )}
        >
          <Icon className={cn("w-5 h-5", methodInfo.color)} />
          <span className={cn("font-semibold", methodInfo.color)}>
            {methodInfo.title}
          </span>
        </div>

        <div className="p-3 space-y-4">
          {/* Description */}
          <p className="text-sm text-muted-foreground">
            {methodInfo.description}
          </p>

          {/* How It Works */}
          <div>
            <h4 className="text-xs font-semibold text-slate-600 uppercase tracking-wide mb-2">
              How It Works
            </h4>
            <ol className="space-y-1.5">
              {methodInfo.howItWorks.map((step, index) => (
                <li
                  key={index}
                  className="flex items-start gap-2 text-xs text-muted-foreground"
                >
                  <span
                    className={cn(
                      "flex-shrink-0 w-4 h-4 rounded-full flex items-center justify-center text-[10px] font-bold",
                      methodInfo.bgColor,
                      methodInfo.color
                    )}
                  >
                    {index + 1}
                  </span>
                  <span className="flex-1">{step}</span>
                  {index < methodInfo.howItWorks.length - 1 && (
                    <ArrowRight className="w-3 h-3 text-muted-foreground/40 hidden sm:block" />
                  )}
                </li>
              ))}
            </ol>
          </div>

          {/* Best For */}
          <div>
            <h4 className="text-xs font-semibold text-green-600 uppercase tracking-wide mb-2 flex items-center gap-1">
              <CheckCircle2 className="w-3 h-3" />
              Best For
            </h4>
            <ul className="space-y-1">
              {methodInfo.bestFor.map((item, index) => (
                <li
                  key={index}
                  className="flex items-center gap-2 text-xs text-muted-foreground"
                >
                  <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
                  {item}
                </li>
              ))}
            </ul>
          </div>

          {/* Weak For */}
          {methodInfo.weakFor.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-red-600 uppercase tracking-wide mb-2 flex items-center gap-1">
                <AlertCircle className="w-3 h-3" />
                Weak For
              </h4>
              <ul className="space-y-1">
                {methodInfo.weakFor.map((item, index) => (
                  <li
                    key={index}
                    className="flex items-center gap-2 text-xs text-muted-foreground"
                  >
                    <span className="w-1.5 h-1.5 rounded-full bg-red-400" />
                    {item}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
};

export default MethodInfoPopup;
