"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { CircleAlert, CircleCheck, Info, Save, WandSparkles } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Controller, useForm, useWatch, type FieldError } from "react-hook-form";

import type { OperatorHook } from "@/entities/hook";
import { ApiError, describeError } from "@/shared/api";
import { formatWasmPages } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import { Label } from "@/shared/ui/label";
import { StringListInput } from "@/shared/ui/string-list-input";
import { Textarea } from "@/shared/ui/textarea";

import { useCreateHook, useReplaceHook } from "../api/use-save-hook";
import {
  emptyHookForm,
  formatJson,
  formFromHook,
  hookFormSchema,
  specFromForm,
  type HookFormValues,
} from "../model/form";

type Props = {
  hook?: OperatorHook;
  onCreated?: (hook: OperatorHook) => void;
};

type Notice = { tone: "success" | "info"; text: string };

const LOCKED_HINT = "Уже разрешено: убрать нельзя, скрипты тенантов могут на это опираться";

function Field({
  id,
  label,
  hint,
  error,
  action,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  error?: FieldError;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex min-h-8 items-end justify-between gap-2">
        <Label htmlFor={id}>{label}</Label>
        {action}
      </div>
      {children}
      {error?.message ? (
        <p id={`${id}-error`} className="text-destructive text-sm">
          {error.message}
        </p>
      ) : (
        hint && <p className="text-muted-foreground text-xs">{hint}</p>
      )}
    </div>
  );
}

function ServerError({ error }: { error: unknown }) {
  const narrowed = error instanceof ApiError && error.status === 409;
  return (
    <div role="alert" className="border-destructive/50 text-destructive flex gap-3 rounded-md border p-4 text-sm">
      <CircleAlert className="mt-0.5 size-4 shrink-0" />
      <div className="flex flex-col gap-1">
        <p className="font-medium">{error instanceof Error ? error.message : describeError(error)}</p>
        {error instanceof ApiError && error.detail && (
          <p className="font-mono text-xs break-all whitespace-pre-wrap">
            {narrowed ? `Убраны: ${error.detail}` : error.detail}
          </p>
        )}
      </div>
    </div>
  );
}

export function HookForm({ hook, onCreated }: Props) {
  const editing = hook !== undefined;
  const ids = useId();
  const id = (field: keyof HookFormValues) => `${ids}-${field}`;
  const create = useCreateHook();
  const replace = useReplaceHook(hook?.name ?? "");
  const [notice, setNotice] = useState<Notice | null>(null);
  const [serverError, setServerError] = useState<unknown>(null);

  const form = useForm<HookFormValues>({
    resolver: zodResolver(hookFormSchema),
    defaultValues: hook ? formFromHook(hook) : emptyHookForm,
  });
  const { register, control, handleSubmit, formState, setValue, getValues, setError, reset } = form;
  const errors = formState.errors;
  const pages = useWatch({ control, name: "memory_max_pages" });

  async function onSubmit(values: HookFormValues) {
    setNotice(null);
    setServerError(null);
    try {
      if (!hook) {
        const created = await create.mutateAsync({ name: values.name, ...specFromForm(values) });
        onCreated?.(created);
        return;
      }
      const saved = await replace.mutateAsync(specFromForm(values));
      reset(formFromHook(saved));
      setNotice(
        saved.def_version === hook.def_version
          ? { tone: "info", text: "Изменений нет" }
          : { tone: "success", text: `Сохранено. Версия определения: ${saved.def_version}` },
      );
    } catch (error) {
      if (!editing && error instanceof ApiError && error.status === 409) {
        setError("name", { message: error.message }, { shouldFocus: true });
        return;
      }
      setServerError(error);
    }
  }

  const jsonField = (field: "input_schema" | "output_schema" | "sample_input", label: string, hint: string) => (
    <Field
      id={id(field)}
      label={label}
      hint={hint}
      error={errors[field]}
      action={
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setValue(field, formatJson(getValues(field)), { shouldDirty: true })}
        >
          <WandSparkles />
          Форматировать
        </Button>
      }
    >
      <Textarea
        id={id(field)}
        rows={8}
        spellCheck={false}
        className="max-h-96 font-mono text-xs"
        aria-invalid={errors[field] ? true : undefined}
        aria-describedby={errors[field] ? `${id(field)}-error` : undefined}
        {...register(field)}
      />
    </Field>
  );

  return (
    <form onSubmit={handleSubmit(onSubmit)} noValidate className="flex max-w-3xl flex-col gap-6">
      {serverError !== null && <ServerError error={serverError} />}
      {notice && (
        <div
          role="status"
          className="text-muted-foreground flex items-center gap-2 rounded-md border p-3 text-sm"
        >
          {notice.tone === "success" ? <CircleCheck className="size-4" /> : <Info className="size-4" />}
          {notice.text}
        </div>
      )}

      {!editing && (
        <Field
          id={id("name")}
          label="Имя хука"
          hint="Например checkout.discount. Имя потом изменить нельзя."
          error={errors.name}
        >
          <Input
            id={id("name")}
            className="font-mono"
            autoComplete="off"
            aria-invalid={errors.name ? true : undefined}
            aria-describedby={errors.name ? `${id("name")}-error` : undefined}
            {...register("name")}
          />
        </Field>
      )}

      <div className="grid gap-6 sm:grid-cols-2">
        <Field id={id("timeout_ms")} label="Лимит времени, мс" hint="От 1 до 30000" error={errors.timeout_ms}>
          <Input
            id={id("timeout_ms")}
            type="number"
            inputMode="numeric"
            aria-invalid={errors.timeout_ms ? true : undefined}
            aria-describedby={errors.timeout_ms ? `${id("timeout_ms")}-error` : undefined}
            {...register("timeout_ms", { valueAsNumber: true })}
          />
        </Field>
        <Field
          id={id("memory_max_pages")}
          label="Лимит памяти, страниц по 64 КиБ"
          hint={`От 16 до 16384${Number.isFinite(pages) ? ` · ${formatWasmPages(pages)}` : ""}`}
          error={errors.memory_max_pages}
        >
          <Input
            id={id("memory_max_pages")}
            type="number"
            inputMode="numeric"
            aria-invalid={errors.memory_max_pages ? true : undefined}
            aria-describedby={errors.memory_max_pages ? `${id("memory_max_pages")}-error` : undefined}
            {...register("memory_max_pages", { valueAsNumber: true })}
          />
        </Field>
      </div>

      {jsonField("input_schema", "Схема входа (JSON Schema 2020-12)", "Что оператор передаёт в вызов хука.")}
      {jsonField("output_schema", "Схема выхода (JSON Schema 2020-12)", "Что обязан вернуть скрипт тенанта.")}
      {jsonField(
        "sample_input",
        "Пример входа",
        "Должен проходить схему входа: на нём проверяют каждый загруженный скрипт.",
      )}

      <Field
        id={id("allowed_host_functions")}
        label="Разрешённые host-функции"
        hint="Список может только расти: после сохранения значение убрать нельзя."
      >
        <Controller
          control={control}
          name="allowed_host_functions"
          render={({ field }) => (
            <StringListInput
              id={id("allowed_host_functions")}
              value={field.value}
              onChange={field.onChange}
              locked={hook?.allowed_host_functions}
              lockedHint={LOCKED_HINT}
            />
          )}
        />
      </Field>

      <Field
        id={id("allowed_effect_types")}
        label="Разрешённые типы эффектов"
        hint="Список может только расти: после сохранения значение убрать нельзя."
      >
        <Controller
          control={control}
          name="allowed_effect_types"
          render={({ field }) => (
            <StringListInput
              id={id("allowed_effect_types")}
              value={field.value}
              onChange={field.onChange}
              locked={hook?.allowed_effect_types}
              lockedHint={LOCKED_HINT}
            />
          )}
        />
      </Field>

      <Button type="submit" disabled={formState.isSubmitting} className="w-fit">
        <Save />
        {formState.isSubmitting ? "Сохранение…" : editing ? "Сохранить" : "Создать хук"}
      </Button>
    </form>
  );
}
