"use client";

import { Check, Copy, Plus, TriangleAlert } from "lucide-react";
import { useId, useState, type FormEvent } from "react";

import { KEY_NAME_MAX, type ApiKeyCreated } from "@/entities/api-key";
import { ApiError, describeError } from "@/shared/api";
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
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";

import { useIssueApiKey } from "../api/use-issue-api-key";

function nameError(name: string): string | null {
  if (!name) return "Укажите имя ключа";
  if (name.length > KEY_NAME_MAX) return `Не длиннее ${KEY_NAME_MAX} символов`;
  return null;
}

function IssuedToken({ issued }: { issued: ApiKeyCreated }) {
  const [copied, setCopied] = useState<boolean | null>(null);

  async function copy() {
    try {
      await navigator.clipboard.writeText(issued.token);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  return (
    <>
      <DialogTitle>Ключ {issued.name} выпущен</DialogTitle>
      <DialogDescription asChild>
        <div>
          <p className="text-foreground flex gap-2">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            Скопируйте токен сейчас: больше его не покажут. Если он потеряется, выпустите новый ключ, а этот отзовите.
          </p>
        </div>
      </DialogDescription>
      <div className="flex items-center gap-2">
        <code aria-label="Токен ключа" className="bg-muted flex-1 rounded-md border px-3 py-2 font-mono text-xs break-all">
          {issued.token}
        </code>
        <Button variant="outline" onClick={copy}>
          {copied ? <Check /> : <Copy />}
          {copied ? "Скопировано" : "Скопировать"}
        </Button>
      </div>
      {copied === false && (
        <p role="alert" className="text-destructive text-sm">
          Не удалось скопировать. Выделите токен и скопируйте вручную.
        </p>
      )}
      <DialogFooter>
        <DialogClose asChild>
          <Button>Готово</Button>
        </DialogClose>
      </DialogFooter>
    </>
  );
}

export function IssueApiKeyDialog() {
  const inputId = useId();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const issue = useIssueApiKey();

  function changeOpen(next: boolean) {
    if (issue.isPending) return;
    if (next) {
      setName("");
      setFieldError(null);
      issue.reset();
    }
    setOpen(next);
  }

  function submit(event: FormEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    const invalid = nameError(trimmed);
    setFieldError(invalid);
    if (invalid) return;
    issue.mutate(trimmed, {
      onError: (error) => {
        if (error instanceof ApiError && error.status === 409) setFieldError(error.message);
      },
    });
  }

  const conflict = issue.error instanceof ApiError && issue.error.status === 409;

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus />
          Выпустить ключ
        </Button>
      </DialogTrigger>
      <DialogContent onInteractOutside={(event) => issue.isSuccess && event.preventDefault()}>
        {issue.isSuccess ? (
          <IssuedToken issued={issue.data} />
        ) : (
          <>
            <DialogTitle>Новый ключ установки</DialogTitle>
            <DialogDescription>
              Ключ даёт полный доступ к API оператора и к вызовам хуков. Токен покажем один раз.
            </DialogDescription>
            <form id={`${inputId}-form`} onSubmit={submit} noValidate className="flex flex-col gap-2">
              <Label htmlFor={inputId}>Имя ключа</Label>
              <Input
                id={inputId}
                autoComplete="off"
                value={name}
                aria-invalid={fieldError ? true : undefined}
                aria-describedby={fieldError ? `${inputId}-error` : undefined}
                onChange={(event) => {
                  setName(event.target.value);
                  setFieldError(null);
                }}
              />
              {fieldError ? (
                <p id={`${inputId}-error`} className="text-destructive text-sm">
                  {fieldError}
                </p>
              ) : (
                <p className="text-muted-foreground text-xs">Например shop-backend: по имени видно, где ключ используется.</p>
              )}
            </form>
            {issue.isError && !conflict && (
              <p role="alert" className="text-destructive text-sm">
                {describeError(issue.error)}
              </p>
            )}
            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline" disabled={issue.isPending}>
                  Отмена
                </Button>
              </DialogClose>
              <Button type="submit" form={`${inputId}-form`} disabled={issue.isPending}>
                {issue.isPending ? "Выпускаем…" : "Выпустить"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
