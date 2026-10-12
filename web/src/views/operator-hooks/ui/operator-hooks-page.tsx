"use client";

import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import Link from "next/link";

import { operatorHookQueries } from "@/entities/hook";
import { routes } from "@/shared/config/routes";
import { formatDateTime, formatWasmPages } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { EmptyState, ErrorState, LoadingRows } from "@/shared/ui/query-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/ui/table";

export function OperatorHooksPage() {
  const { data: hooks, error, isPending } = useQuery(operatorHookQueries.list());

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Хуки</h1>
          <p className="text-muted-foreground text-sm">
            Точки расширения продукта, на которые тенанты загружают свои обработчики.
          </p>
        </div>
        <Button asChild>
          <Link href={routes.operator.newHook}>
            <Plus />
            Создать хук
          </Link>
        </Button>
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : hooks.length === 0 ? (
        <EmptyState>Хуков пока нет. Создайте первый, например checkout.discount.</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Хук</TableHead>
              <TableHead className="text-right">Версия определения</TableHead>
              <TableHead>Лимит времени</TableHead>
              <TableHead>Лимит памяти</TableHead>
              <TableHead>Изменён</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {hooks.map((hook) => (
              <TableRow key={hook.name}>
                <TableCell>
                  <Link
                    href={routes.operator.hook(hook.name)}
                    className="font-mono font-medium underline-offset-4 hover:underline"
                  >
                    {hook.name}
                  </Link>
                </TableCell>
                <TableCell className="text-right tabular-nums">{hook.def_version}</TableCell>
                <TableCell>{hook.timeout_ms} мс</TableCell>
                <TableCell>
                  {hook.memory_max_pages} стр. · {formatWasmPages(hook.memory_max_pages)}
                </TableCell>
                <TableCell className="text-muted-foreground">{formatDateTime(hook.updated_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
