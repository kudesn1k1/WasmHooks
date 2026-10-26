"use client";

import { ExternalLink } from "lucide-react";

import { describeError } from "@/shared/api";
import { Button } from "@/shared/ui/button";

import { useOpenTenantConsole } from "../api/use-open-tenant-console";

export function OpenTenantConsoleButton({ externalId }: { externalId: string }) {
  const session = useOpenTenantConsole(externalId);

  return (
    <div className="flex flex-col items-end gap-1">
      <Button
        variant="outline"
        size="sm"
        aria-label={`Открыть консоль ${externalId}`}
        disabled={session.isPending}
        onClick={() => session.mutate(window.open("", "_blank"))}
      >
        <ExternalLink />
        {session.isPending ? "Открываем…" : "Открыть консоль"}
      </Button>
      {session.isError && (
        <p role="alert" className="text-destructive max-w-72 text-right text-xs">
          {describeError(session.error)}
        </p>
      )}
    </div>
  );
}
