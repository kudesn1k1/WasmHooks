"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { hookQueries } from "@/entities/hook";
import { useTenantSession } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { shortHash } from "@/shared/lib/format";
import { Breadcrumbs } from "@/shared/ui/breadcrumbs";
import { Button } from "@/shared/ui/button";
import { InfoPanel } from "@/shared/ui/info-panel";
import { EmptyState, ErrorState, LoadingRows } from "@/shared/ui/query-state";

export function TenantHooksPage() {
  const session = useTenantSession();
  const { data: hooks, error, isPending } = useQuery(hookQueries.list());

  return (
    <div className="flex max-w-245 flex-col gap-6">
      <Breadcrumbs items={[...(session?.tenant ? [{ label: session.tenant }] : []), { label: "Хуки" }]} />
      <div className="flex flex-col gap-4">
        <h1 className="text-[28px] font-semibold tracking-tight">Хуки</h1>
        <p className="text-muted-foreground text-sm">
          Выберите точку расширения, чтобы управлять версиями обработчика.
        </p>
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : hooks.length === 0 ? (
        <EmptyState>Оператор ещё не открыл ни одного хука.</EmptyState>
      ) : (
        <ul className="flex flex-col gap-4">
          {hooks.map(({ hook, binding }) => (
            <li key={hook.name} className="flex items-start justify-between gap-4 rounded-md border px-6 py-6">
              <div className="flex flex-col gap-2">
                <h2 className="text-lg font-semibold">{hook.name}</h2>
                <p className="text-sm">
                  {binding?.active_module_hash
                    ? `Активная версия: ${shortHash(binding.active_module_hash)}`
                    : "Активной версии нет: хук работает без обработчика"}
                </p>
              </div>
              <Button asChild>
                <Link href={routes.tenant.hook(hook.name)} aria-label={`Открыть ${hook.name}`}>
                  Открыть
                </Link>
              </Button>
            </li>
          ))}
        </ul>
      )}

      <InfoPanel title="Как работает обновление">
        <p>
          Загрузите готовый .wasm, дождитесь проверки и активируйте версию. До активации продолжает работать
          текущий обработчик.
        </p>
      </InfoPanel>
    </div>
  );
}
