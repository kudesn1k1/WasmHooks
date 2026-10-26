"use client";

import { useState } from "react";

import type { ApiKey } from "@/entities/api-key";
import { describeError } from "@/shared/api";
import { Button } from "@/shared/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
  DialogTrigger,
} from "@/shared/ui/dialog";

import { useRevokeApiKey } from "../api/use-revoke-api-key";

export function RevokeApiKeyDialog({ apiKey }: { apiKey: ApiKey }) {
  const [open, setOpen] = useState(false);
  const revoke = useRevokeApiKey(() => setOpen(false));

  function changeOpen(next: boolean) {
    if (revoke.isPending) return;
    if (!next) revoke.reset();
    setOpen(next);
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" aria-label={`Отозвать ${apiKey.name}`}>
          Отозвать
        </Button>
      </DialogTrigger>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle>Отозвать ключ {apiKey.name}?</DialogTitle>
        <DialogDescription asChild>
          <div>
            <p>
              Запросы с этим ключом сразу начнут получать 401: и к API оператора, и вызовы хуков через data
              plane. Вернуть ключ нельзя, только выпустить новый.
            </p>
            <p>Если вы вошли в консоль этим ключом, консоль попросит войти заново.</p>
          </div>
        </DialogDescription>
        {revoke.isError && (
          <p role="alert" className="text-destructive text-sm">
            {describeError(revoke.error)}
          </p>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={revoke.isPending}>
              Отмена
            </Button>
          </DialogClose>
          <Button variant="destructive" disabled={revoke.isPending} onClick={() => revoke.mutate(apiKey.id)}>
            {revoke.isPending ? "Отзываем…" : "Отозвать"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
