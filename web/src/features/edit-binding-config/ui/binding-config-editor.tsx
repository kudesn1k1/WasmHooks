"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Plus, Trash2 } from "lucide-react";
import { useId, useState } from "react";
import { useFieldArray, useForm } from "react-hook-form";

import type { Binding } from "@/entities/hook";
import { describeError } from "@/shared/api";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

import { useSaveConfig } from "../api/use-save-config";
import {
  configFormSchema,
  configFromForm,
  formFromConfig,
  sameConfig,
  type ConfigFormValues,
} from "../model/form";

type Props = {
  hook: string;
  binding?: Binding | null;
};

export function BindingConfigEditor({ hook, binding }: Props) {
  const ids = useId();
  const save = useSaveConfig(hook);
  const [notice, setNotice] = useState<string | null>(null);
  const current = binding?.config ?? {};

  const { register, control, handleSubmit, formState, reset } = useForm<ConfigFormValues>({
    resolver: zodResolver(configFormSchema),
    defaultValues: formFromConfig(binding?.config),
  });
  const { fields, append, remove } = useFieldArray({ control, name: "rows" });
  const rowErrors = formState.errors.rows;

  async function onSubmit(values: ConfigFormValues) {
    setNotice(null);
    save.reset();
    const config = configFromForm(values);
    if (sameConfig(config, current)) {
      reset(formFromConfig(current));
      setNotice("Изменений нет");
      return;
    }
    try {
      const saved = await save.mutateAsync(config);
      reset(formFromConfig(saved.config));
      setNotice(
        binding && saved.config_version === binding.config_version
          ? "Изменений нет"
          : `Сохранено. Версия конфига: ${saved.config_version}`,
      );
    } catch {
      return;
    }
  }

  return (
    <section className="flex flex-col gap-4 rounded-md border px-6 py-6">
      <div className="flex flex-col gap-1">
        <h2 className="text-lg font-semibold">Параметры скрипта</h2>
        <p className="text-muted-foreground text-sm">
          Скрипт читает их через config_get. Значения — строки: числа вводите как текст, скрипт разберёт их сам.
          {binding ? ` Версия конфига: ${binding.config_version}.` : " Параметры можно задать до активации версии."}
        </p>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} noValidate className="flex flex-col gap-3">
        {fields.length > 0 ? (
          <div className="text-muted-foreground grid grid-cols-[1fr_1fr_auto] gap-3 text-xs">
            <span>Ключ</span>
            <span>Значение</span>
            <span className="w-9" />
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">Параметров нет.</p>
        )}
        {fields.map((field, index) => {
          const keyError = rowErrors?.[index]?.key?.message;
          return (
            <div key={field.id} className="grid grid-cols-[1fr_1fr_auto] items-start gap-3">
              <div className="flex flex-col gap-1">
                <Input
                  aria-label={`Ключ ${index + 1}`}
                  className="font-mono"
                  autoComplete="off"
                  aria-invalid={keyError ? true : undefined}
                  aria-describedby={keyError ? `${ids}-error-${index}` : undefined}
                  {...register(`rows.${index}.key`)}
                />
                {keyError && (
                  <p id={`${ids}-error-${index}`} className="text-destructive text-xs">
                    {keyError}
                  </p>
                )}
              </div>
              <Input
                aria-label={`Значение ${index + 1}`}
                className="font-mono"
                autoComplete="off"
                {...register(`rows.${index}.value`)}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={`Удалить параметр ${index + 1}`}
                onClick={() => remove(index)}
              >
                <Trash2 />
              </Button>
            </div>
          );
        })}

        <div className="flex flex-wrap items-center gap-3 pt-1">
          <Button type="button" variant="outline" onClick={() => append({ key: "", value: "" })}>
            <Plus />
            Добавить параметр
          </Button>
          <Button type="submit" disabled={formState.isSubmitting}>
            {formState.isSubmitting ? "Сохранение…" : "Сохранить параметры"}
          </Button>
          {notice && !formState.isDirty && (
            <p role="status" className="text-muted-foreground text-sm">
              {notice}
            </p>
          )}
        </div>
        {save.isError && (
          <p role="alert" className="text-destructive text-sm">
            {describeError(save.error)}
          </p>
        )}
        <p className="text-muted-foreground text-xs">
          Обновление исполнителей может занять некоторое время. Модуль с новыми параметрами повторно не проверяется.
        </p>
      </form>
    </section>
  );
}
