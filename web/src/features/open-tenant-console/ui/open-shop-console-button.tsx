"use client";

import { ExternalLink } from "lucide-react";

import { describeError } from "@/shared/api";
import { Button } from "@/shared/ui/button";
import { RequestError } from "@/shared/ui/request-error";

import { useOpenShopConsole } from "../api/use-open-tenant-console";

export function OpenShopConsoleButton({ shopId }: { shopId: string }) {
  const session = useOpenShopConsole(shopId);

  return (
    <div className="space-y-2">
      <Button variant="outline" disabled={session.isPending} onClick={() => session.mutate(window.open("", "_blank"))}>
        {session.isPending ? "Открываем…" : "Мои скрипты"}
        <ExternalLink />
      </Button>
      {session.isError && (
        <RequestError title="Не удалось открыть консоль">{describeError(session.error)}. Попробуйте ещё раз.</RequestError>
      )}
    </div>
  );
}
