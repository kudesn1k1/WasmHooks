"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import Link from "next/link";

import { hookQueries, type TenantHook } from "@/entities/hook";
import { UploadModuleForm } from "@/features/upload-module";
import { routes } from "@/shared/config/routes";
import { formatWasmPages } from "@/shared/lib/format";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/shared/ui/card";
import { ErrorState, LoadingRows } from "@/shared/ui/query-state";
import { ModuleVersions } from "@/widgets/module-versions";

function HookFacts({ hook, binding }: TenantHook) {
  const facts: [string, string][] = [
    ["Лимит времени", `${hook.timeout_ms} мс`],
    ["Лимит памяти", formatWasmPages(hook.memory_max_pages)],
    ["Версия определения", String(hook.def_version)],
    ["Разрешённые эффекты", hook.allowed_effect_types.join(", ") || "нет"],
    ["Конфиг", binding ? Object.keys(binding.config).join(", ") || "пустой" : "—"],
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3 lg:grid-cols-5">
      {facts.map(([label, value]) => (
        <div key={label}>
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="font-medium">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function TenantHookPage({ hook: name }: { hook: string }) {
  const { data, error, isPending } = useQuery(hookQueries.detail(name));

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <Link
          href={routes.tenant.hooks}
          className="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-sm"
        >
          <ArrowLeft className="size-4" />
          Все хуки
        </Link>
        <h1 className="font-mono text-2xl font-semibold tracking-tight">{name}</h1>
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : (
        <>
          <HookFacts {...data} />
          <Card>
            <CardHeader>
              <CardTitle>Версии модуля</CardTitle>
              <CardDescription>
                Активна не версия, а привязка к ней: откат — это активация более старой проверенной
                версии.
              </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-6">
              <UploadModuleForm hook={name} />
              <ModuleVersions hook={name} binding={data.binding} />
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
