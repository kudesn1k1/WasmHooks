import { ApiError } from "@/shared/api";
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

export function ErrorState({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : "Неизвестная ошибка";
  const detail = error instanceof ApiError ? error.detail : undefined;
  return (
    <div role="alert" className="border-destructive/50 text-destructive rounded-md border p-4 text-sm">
      <p className="font-medium">Не удалось загрузить данные: {message}</p>
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
