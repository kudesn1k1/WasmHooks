"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import {
  INVOCATIONS_POLL_MS,
  invocationQueries,
  OutcomeBadge,
  outcomes,
  type Outcome,
} from "@/entities/invocation";
import { formatDateTime, shortHash } from "@/shared/lib/format";
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

export function InvocationLog() {
  const [outcome, setOutcome] = useState<Outcome | undefined>();
  const { data, error, isPending, isFetching } = useQuery(invocationQueries.list({ outcome }));

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label="Фильтр по исходу" className="flex flex-wrap gap-1">
          {[undefined, ...outcomes].map((value) => (
            <Button
              key={value ?? "all"}
              size="sm"
              variant={outcome === value ? "default" : "outline"}
              aria-pressed={outcome === value}
              className={value ? "font-mono" : undefined}
              onClick={() => setOutcome(value)}
            >
              {value ?? "все"}
            </Button>
          ))}
        </div>
        <span className="text-muted-foreground ml-auto text-xs" aria-live="polite">
          {isFetching ? "обновление…" : `обновляется каждые ${INVOCATIONS_POLL_MS / 1000} с`}
        </span>
      </div>

      {isPending ? (
        <LoadingRows rows={8} />
      ) : error ? (
        <ErrorState error={error} />
      ) : data.length === 0 ? (
        <EmptyState>Вызовов с таким исходом нет.</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Время</TableHead>
              <TableHead>Хук</TableHead>
              <TableHead>Исход</TableHead>
              <TableHead className="text-right">Длительность</TableHead>
              <TableHead>Версия</TableHead>
              <TableHead>Ошибка</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((inv) => (
              <TableRow key={inv.seq} title={inv.logs.join("\n") || undefined}>
                <TableCell>{formatDateTime(inv.ts)}</TableCell>
                <TableCell className="font-mono">{inv.hook}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <OutcomeBadge outcome={inv.outcome} />
                    {inv.reason && (
                      <span className="text-muted-foreground font-mono text-xs">{inv.reason}</span>
                    )}
                  </div>
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {inv.duration_ms.toFixed(2)} мс
                  {inv.cold_start && (
                    <span className="text-muted-foreground ml-1 text-xs" title="Холодный старт">
                      ❄
                    </span>
                  )}
                </TableCell>
                <TableCell className="font-mono">
                  {inv.module_hash ? shortHash(inv.module_hash) : "—"}
                </TableCell>
                <TableCell className="text-muted-foreground max-w-xs truncate" title={inv.error ?? undefined}>
                  {inv.error ?? ""}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
