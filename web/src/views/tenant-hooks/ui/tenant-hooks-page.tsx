"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { hookQueries } from "@/entities/hook";
import { routes } from "@/shared/config/routes";
import { formatDateTime, formatWasmPages, shortHash } from "@/shared/lib/format";
import { Badge } from "@/shared/ui/badge";
import { EmptyState, ErrorState, LoadingRows } from "@/shared/ui/query-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/ui/table";

export function TenantHooksPage() {
  const { data: hooks, error, isPending } = useQuery(hookQueries.list());

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Хуки</h1>
        <p className="text-muted-foreground text-sm">
          Точки расширения, на которые можно загрузить свой обработчик.
        </p>
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : hooks.length === 0 ? (
        <EmptyState>Оператор ещё не открыл ни одного хука.</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Хук</TableHead>
              <TableHead>Лимит времени</TableHead>
              <TableHead>Лимит памяти</TableHead>
              <TableHead>Активная версия</TableHead>
              <TableHead>Изменена</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {hooks.map(({ hook, binding }) => (
              <TableRow key={hook.name}>
                <TableCell>
                  <Link
                    href={routes.tenant.hook(hook.name)}
                    className="font-mono font-medium underline-offset-4 hover:underline"
                  >
                    {hook.name}
                  </Link>
                </TableCell>
                <TableCell>{hook.timeout_ms} мс</TableCell>
                <TableCell>{formatWasmPages(hook.memory_max_pages)}</TableCell>
                <TableCell>
                  {binding?.active_module_hash ? (
                    <span className="font-mono">{shortHash(binding.active_module_hash)}</span>
                  ) : (
                    <Badge variant="outline">нет обработчика</Badge>
                  )}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {binding ? formatDateTime(binding.updated_at) : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
