"use client";

import { useQuery } from "@tanstack/react-query";

import { formatQuota, tenantQueries } from "@/entities/tenant";
import { OpenTenantConsoleButton } from "@/features/open-tenant-console";
import { TenantDialog } from "@/features/tenant-editor";
import { formatDateTime } from "@/shared/lib/format";
import { EmptyState, ErrorState, LoadingRows } from "@/shared/ui/query-state";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/ui/table";

export function OperatorTenantsPage() {
  const { data: tenants, error, isPending } = useQuery(tenantQueries.list());

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Тенанты</h1>
          <p className="text-muted-foreground text-sm">
            Клиенты продукта, которые загружают свои обработчики хуков. Квота 0 означает значение по умолчанию
            data plane.
          </p>
        </div>
        <TenantDialog />
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : tenants.length === 0 ? (
        <EmptyState>Тенантов пока нет. Добавьте первого, например merchant-a.</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Тенант</TableHead>
              <TableHead>Запросов в секунду</TableHead>
              <TableHead>Параллельных вызовов</TableHead>
              <TableHead>Изменён</TableHead>
              <TableHead className="text-right">Действия</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tenants.map((tenant) => (
              <TableRow key={tenant.external_id}>
                <TableCell>
                  <p className="font-mono font-medium">{tenant.external_id}</p>
                  <p className="text-muted-foreground text-xs">{tenant.name}</p>
                </TableCell>
                <TableCell>{formatQuota(tenant.rate_limit_rps, "rps")}</TableCell>
                <TableCell>{formatQuota(tenant.concurrency_limit, "шт.")}</TableCell>
                <TableCell className="text-muted-foreground">{formatDateTime(tenant.updated_at)}</TableCell>
                <TableCell>
                  <div className="flex items-start justify-end gap-2">
                    <TenantDialog tenant={tenant} />
                    <OpenTenantConsoleButton externalId={tenant.external_id} />
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
