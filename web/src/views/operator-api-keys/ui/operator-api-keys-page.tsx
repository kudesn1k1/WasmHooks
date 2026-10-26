"use client";

import { useQuery } from "@tanstack/react-query";

import { apiKeyQueries } from "@/entities/api-key";
import { IssueApiKeyDialog } from "@/features/issue-api-key";
import { RevokeApiKeyDialog } from "@/features/revoke-api-key";
import { formatDateTime } from "@/shared/lib/format";
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

export function OperatorApiKeysPage() {
  const { data, error, isPending } = useQuery(apiKeyQueries.list());
  const keys = data?.toSorted((a, b) => b.created_at.localeCompare(a.created_at));

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Ключи установки</h1>
          <p className="text-muted-foreground text-sm">
            Дают доступ к API оператора и к вызовам хуков. Отозванные ключи остаются в списке.
          </p>
        </div>
        <IssueApiKeyDialog />
      </div>

      {isPending ? (
        <LoadingRows />
      ) : error ? (
        <ErrorState error={error} />
      ) : keys?.length === 0 ? (
        <EmptyState>Ключей нет. Выпустите ключ или создайте его командой controlplane apikey create.</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Ключ</TableHead>
              <TableHead>Выпущен</TableHead>
              <TableHead>Статус</TableHead>
              <TableHead className="text-right">Действие</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {keys?.map((key) => (
              <TableRow key={key.id}>
                <TableCell>
                  <p className="font-medium">{key.name}</p>
                  <p className="text-muted-foreground font-mono text-xs">{key.id}</p>
                </TableCell>
                <TableCell className="text-muted-foreground">{formatDateTime(key.created_at)}</TableCell>
                <TableCell>
                  {key.revoked_at ? (
                    <span className="text-muted-foreground">Отозван {formatDateTime(key.revoked_at)}</span>
                  ) : (
                    <Badge variant="outline">Активен</Badge>
                  )}
                </TableCell>
                <TableCell className="text-right">{key.revoked_at ? "–" : <RevokeApiKeyDialog apiKey={key} />}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
