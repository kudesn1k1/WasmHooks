"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Pencil, Plus } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useForm, type FieldError } from "react-hook-form";

import type { Tenant } from "@/entities/tenant";
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

import { useCreateTenant, usePatchTenant } from "../api/use-save-tenant";
import {
  emptyTenantForm,
  formFromTenant,
  tenantChanges,
  tenantFormSchema,
  type TenantFormValues,
} from "../model/form";

const QUOTA_HINT = "0 — значение по умолчанию data plane";

function Field({
  id,
  label,
  hint,
  error,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  error?: FieldError;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
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

export function TenantDialog({ tenant }: { tenant?: Tenant }) {
  const editing = tenant !== undefined;
  const ids = useId();
  const id = (field: keyof TenantFormValues) => `${ids}-${field}`;
  const [open, setOpen] = useState(false);
  const [serverError, setServerError] = useState<unknown>(null);
  const create = useCreateTenant();
  const patch = usePatchTenant(tenant?.external_id ?? "");

  const { register, handleSubmit, formState, reset, setError } = useForm<TenantFormValues>({
    resolver: zodResolver(tenantFormSchema),
    defaultValues: tenant ? formFromTenant(tenant) : emptyTenantForm,
  });
  const errors = formState.errors;

  function changeOpen(next: boolean) {
    if (formState.isSubmitting) return;
    if (next) {
      reset(tenant ? formFromTenant(tenant) : emptyTenantForm);
      setServerError(null);
    }
    setOpen(next);
  }

  async function onSubmit(values: TenantFormValues) {
    setServerError(null);
    try {
      if (!tenant) {
        await create.mutateAsync(values);
      } else {
        const changes = tenantChanges(tenant, values);
        if (Object.keys(changes).length > 0) await patch.mutateAsync(changes);
      }
      setOpen(false);
    } catch (error) {
      if (!editing && error instanceof ApiError && error.status === 409) {
        setError("external_id", { message: error.message }, { shouldFocus: true });
        return;
      }
      setServerError(error);
    }
  }

  const invalid = (field: keyof TenantFormValues) =>
    errors[field] ? { "aria-invalid": true, "aria-describedby": `${id(field)}-error` } : {};

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild>
        {editing ? (
          <Button variant="outline" size="sm" aria-label={`Изменить ${tenant.external_id}`}>
            <Pencil />
            Изменить
          </Button>
        ) : (
          <Button>
            <Plus />
            Добавить тенанта
          </Button>
        )}
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>{editing ? `Тенант ${tenant.external_id}` : "Новый тенант"}</DialogTitle>
        <DialogDescription>
          {editing
            ? "external_id изменить нельзя: по нему data plane узнаёт тенанта в вызовах."
            : "Тенант появится в снимке конфигурации data plane сразу после создания."}
        </DialogDescription>

        <form id={`${ids}-form`} onSubmit={handleSubmit(onSubmit)} noValidate className="flex flex-col gap-4">
          {!editing && (
            <Field
              id={id("external_id")}
              label="external_id"
              hint="Идентификатор тенанта в вашем продукте, например merchant-a."
              error={errors.external_id}
            >
              <Input id={id("external_id")} className="font-mono" autoComplete="off" {...invalid("external_id")} {...register("external_id")} />
            </Field>
          )}
          <Field id={id("name")} label="Название" error={errors.name}>
            <Input id={id("name")} autoComplete="off" {...invalid("name")} {...register("name")} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field id={id("rate_limit_rps")} label="Лимит запросов в секунду" hint={QUOTA_HINT} error={errors.rate_limit_rps}>
              <Input
                id={id("rate_limit_rps")}
                type="number"
                inputMode="numeric"
                min={0}
                {...invalid("rate_limit_rps")}
                {...register("rate_limit_rps", { valueAsNumber: true })}
              />
            </Field>
            <Field
              id={id("concurrency_limit")}
              label="Параллельных вызовов"
              hint={QUOTA_HINT}
              error={errors.concurrency_limit}
            >
              <Input
                id={id("concurrency_limit")}
                type="number"
                inputMode="numeric"
                min={0}
                {...invalid("concurrency_limit")}
                {...register("concurrency_limit", { valueAsNumber: true })}
              />
            </Field>
          </div>
          <p className="text-muted-foreground text-xs">Квоты попадут в снимок, применять их data plane начнёт в MVP.</p>
        </form>

        {serverError !== null && (
          <p role="alert" className="text-destructive text-sm">
            {describeError(serverError)}
          </p>
        )}

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={formState.isSubmitting}>
              Отмена
            </Button>
          </DialogClose>
          <Button type="submit" form={`${ids}-form`} disabled={formState.isSubmitting || (editing && !formState.isDirty)}>
            {formState.isSubmitting ? "Сохранение…" : editing ? "Сохранить" : "Создать"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
