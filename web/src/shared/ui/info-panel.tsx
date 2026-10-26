import type { ReactNode } from "react";

import { cn } from "@/shared/lib/utils";

type Props = {
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  role?: "status" | "alert";
  className?: string;
};

export function InfoPanel({ title, children, action, role, className }: Props) {
  return (
    <section role={role} className={cn("bg-muted flex flex-col gap-3 rounded-md border p-6", className)}>
      <h2 className="font-semibold">{title}</h2>
      {children && <div className="text-muted-foreground flex flex-col gap-1 text-sm">{children}</div>}
      {action && <div className="pt-2">{action}</div>}
    </section>
  );
}
