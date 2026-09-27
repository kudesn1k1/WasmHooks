"use client";

import { useQuery } from "@tanstack/react-query";

import type { Binding } from "@/entities/hook";
import { firstFailedCheck, moduleQueries, ModuleStatusBadge, type Module } from "@/entities/module";
import { ActivateModuleButton } from "@/features/activate-module";
import { formatBytes, formatDateTime, shortHash } from "@/shared/lib/format";
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

type Props = {
  hook: string;
  binding: Binding | null;
};

function ModuleAction({ hook, module, active }: { hook: string; module: Module; active?: Module }) {
  if (module.id === active?.id) return <Badge>активна</Badge>;
  if (module.status !== "validated") return null;
  const older = active !== undefined && module.uploaded_at < active.uploaded_at;
  return <ActivateModuleButton hook={hook} moduleId={module.id} kind={older ? "rollback" : "activate"} />;
}

export function ModuleVersions({ hook, binding }: Props) {
  const { data: modules, error, isPending } = useQuery(moduleQueries.byHook(hook));

  if (isPending) return <LoadingRows />;
  if (error) return <ErrorState error={error} />;
  if (modules.length === 0) {
    return <EmptyState>Версий пока нет. Загрузите первый .wasm-модуль.</EmptyState>;
  }

  const active = modules.find((m) => m.id === binding?.active_module_id);

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Версия</TableHead>
          <TableHead>Размер</TableHead>
          <TableHead>Статус</TableHead>
          <TableHead>Загружена</TableHead>
          <TableHead className="text-right">
            <span className="sr-only">Действия</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {modules.map((module) => {
          const failed = firstFailedCheck(module);
          return (
            <TableRow key={module.id} data-state={module.id === active?.id ? "selected" : undefined}>
              <TableCell className="font-mono" title={module.content_hash}>
                {shortHash(module.content_hash)}
              </TableCell>
              <TableCell>{formatBytes(module.size_bytes)}</TableCell>
              <TableCell>
                <div className="flex flex-col gap-1">
                  <ModuleStatusBadge status={module.status} />
                  {module.status === "rejected" && failed && (
                    <span className="text-muted-foreground text-xs whitespace-normal">
                      {failed.name}: {failed.detail}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell>{formatDateTime(module.uploaded_at)}</TableCell>
              <TableCell className="text-right">
                <ModuleAction hook={hook} module={module} active={active} />
              </TableCell>
            </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}
