"use client";

import { useState } from "react";

import type { Binding } from "@/entities/hook";
import type { Module } from "@/entities/module";
import { describeError } from "@/shared/api";
import { shortHash } from "@/shared/lib/format";
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

import { useActivateModule } from "../api/use-activate-module";

type Props = {
  hook: string;
  module: Module;
  activeHash?: string | null;
  onActivated?: (binding: Binding) => void;
};

export function ActivateModuleDialog({ hook, module, activeHash, onActivated }: Props) {
  const [open, setOpen] = useState(false);
  const activate = useActivateModule(hook, (binding) => {
    setOpen(false);
    onActivated?.(binding);
  });
  const target = shortHash(module.content_hash);
  const current = activeHash ? shortHash(activeHash) : null;

  function changeOpen(next: boolean) {
    if (activate.isPending) return;
    if (!next) activate.reset();
    setOpen(next);
  }

  function confirm() {
    activate.mutate(module.id);
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button aria-label={`Активировать ${target}`}>Активировать</Button>
      </DialogTrigger>
      <DialogContent aria-describedby={undefined}>
        <DialogTitle>Активировать {target}?</DialogTitle>
        <DialogDescription asChild>
          <div>
            <p>
              Для {hook} будет выбрана версия {target}
              {current ? ` вместо ${current}.` : "."}
            </p>
            <p>
              Обновление исполнителей может занять некоторое время.
              {current && " Предыдущая версия останется в списке."}
            </p>
          </div>
        </DialogDescription>
        {activate.isError && (
          <p role="alert" className="text-destructive text-sm">
            {describeError(activate.error)}
          </p>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={activate.isPending}>
              Отмена
            </Button>
          </DialogClose>
          <Button disabled={activate.isPending} onClick={confirm}>
            {activate.isPending ? "Активация…" : "Активировать"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
