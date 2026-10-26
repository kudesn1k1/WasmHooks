"use client";

import { ArrowLeft } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";

import { HookForm } from "@/features/hook-editor";
import { routes } from "@/shared/config/routes";

export function OperatorHookNewPage() {
  const router = useRouter();

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <Link
          href={routes.operator.hooks}
          className="text-muted-foreground hover:text-foreground flex w-fit items-center gap-1 text-sm"
        >
          <ArrowLeft className="size-4" />
          Все хуки
        </Link>
        <h1 className="text-2xl font-semibold tracking-tight">Новый хук</h1>
      </div>
      <HookForm onCreated={(hook) => router.push(routes.operator.hook(hook.name))} />
    </div>
  );
}
