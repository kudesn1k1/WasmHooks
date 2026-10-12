"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import Link from "next/link";

import { operatorHookQueries } from "@/entities/hook";
import { HookForm } from "@/features/hook-editor";
import { routes } from "@/shared/config/routes";
import { formatDateTime } from "@/shared/lib/format";
import { ErrorState, LoadingRows } from "@/shared/ui/query-state";

export function OperatorHookPage({ name }: { name: string }) {
  const { data: hook, error, isPending } = useQuery(operatorHookQueries.detail(name));

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <Link
          href={routes.operator.hooks}
          className="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-sm"
        >
          <ArrowLeft className="size-4" />
          Все хуки
        </Link>
        <h1 className="font-mono text-2xl font-semibold tracking-tight">{name}</h1>
        {hook && (
          <p className="text-muted-foreground text-sm">
            Версия определения {hook.def_version} · создан {formatDateTime(hook.created_at)} · изменён{" "}
            {formatDateTime(hook.updated_at)}
          </p>
        )}
      </div>

      {isPending ? (
        <LoadingRows rows={6} />
      ) : error ? (
        <ErrorState error={error} />
      ) : (
        <HookForm key={hook.name} hook={hook} />
      )}
    </div>
  );
}
