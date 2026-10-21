import { Construction } from "lucide-react";

import { ApiError, describeError } from "@/shared/api";
import { Skeleton } from "@/shared/ui/skeleton";

export function LoadingRows({ rows = 3 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-2" aria-busy="true" aria-label="Загрузка">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  );
}

function NotImplementedState({ error }: { error: ApiError }) {
  return (
    <div role="status" className="text-muted-foreground flex items-center gap-3 rounded-md border border-dashed p-4 text-sm">
      <Construction className="size-5 shrink-0" />
      <p>{describeError(error)}. Экран заработает, когда бэкенд реализует эту задачу.</p>
    </div>
  );
}

export function ErrorState({ error }: { error: unknown }) {
  if (error instanceof ApiError && error.status === 501) return <NotImplementedState error={error} />;
  const detail = error instanceof ApiError ? error.detail : undefined;
  return (
    <div role="alert" className="border-destructive/50 text-destructive rounded-md border p-4 text-sm">
      <p className="font-medium">Не удалось загрузить данные: {error instanceof Error ? error.message : "Неизвестная ошибка"}</p>
      {detail && <pre className="mt-2 text-xs whitespace-pre-wrap">{detail}</pre>}
    </div>
  );
}

export function EmptyState({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-muted-foreground rounded-md border border-dashed p-8 text-center text-sm">
      {children}
    </div>
  );
}
