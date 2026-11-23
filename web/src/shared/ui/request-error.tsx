import { TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";

export function RequestError({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div role="alert" className="border-destructive/25 bg-destructive/5 text-destructive flex items-start gap-2 rounded-md border p-3 text-sm wrap-anywhere">
      <TriangleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <div className="space-y-1">
        <p className="font-medium">{title}</p>
        <p className="text-xs">{children}</p>
      </div>
    </div>
  );
}
