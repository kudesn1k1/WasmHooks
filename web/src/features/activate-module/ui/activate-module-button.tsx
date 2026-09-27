"use client";

import { RotateCcw, Zap } from "lucide-react";

import { Button } from "@/shared/ui/button";

import { useActivateModule } from "../api/use-activate-module";

type Props = {
  hook: string;
  moduleId: string;
  kind: "activate" | "rollback";
};

export function ActivateModuleButton({ hook, moduleId, kind }: Props) {
  const activate = useActivateModule(hook);
  const Icon = kind === "rollback" ? RotateCcw : Zap;

  return (
    <div className="flex items-center justify-end gap-2">
      {activate.isError && (
        <span role="alert" className="text-destructive text-xs">
          {activate.error.message}
        </span>
      )}
      <Button
        size="sm"
        variant={kind === "rollback" ? "outline" : "default"}
        disabled={activate.isPending}
        onClick={() => activate.mutate(moduleId)}
      >
        <Icon />
        {kind === "rollback" ? "Откатить" : "Активировать"}
      </Button>
    </div>
  );
}
