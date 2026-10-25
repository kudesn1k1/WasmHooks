"use client";

import { useQuery } from "@tanstack/react-query";

import { hookQueries } from "@/entities/hook";
import { useTenantSession } from "@/features/console-session";
import { routes } from "@/shared/config/routes";
import { shortHash } from "@/shared/lib/format";
import { Breadcrumbs } from "@/shared/ui/breadcrumbs";
import { ErrorState, LoadingRows } from "@/shared/ui/query-state";
import { ModuleVersions } from "@/widgets/module-versions";

export function TenantHookPage({ hook: name }: { hook: string }) {
  const session = useTenantSession();
  const { data, error, isPending } = useQuery(hookQueries.detail(name));
  const activeHash = data?.binding?.active_module_hash;

  return (
    <div className="flex max-w-245 flex-col gap-6">
      <Breadcrumbs
        items={[
          ...(session?.tenant ? [{ label: session.tenant }] : []),
          { label: "Хуки", href: routes.tenant.hooks },
        ]}
      />
      <div className="flex flex-col gap-4">
        <h1 className="text-[28px] font-semibold tracking-tight break-all">{name}</h1>
        <p className="text-muted-foreground text-sm">Загрузка, проверка и активация обработчика</p>
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-4 text-sm">
            <span className="bg-muted rounded-md border px-2.5 py-1.5 text-xs font-medium">
              {activeHash ? `Активная версия: ${shortHash(activeHash)}` : "Активной версии нет"}
            </span>
            <span className="text-muted-foreground">
              {activeHash ? "Выбрана для этого хука" : "Хук отвечает без обработчика, пока вы не активируете версию"}
            </span>
          </div>
          <ModuleVersions hook={name} binding={data.binding} />
        </>
      )}
    </div>
  );
}
