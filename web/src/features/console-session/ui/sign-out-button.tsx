"use client";

import { useQueryClient } from "@tanstack/react-query";
import { LogOut } from "lucide-react";

import { clearToken, useSessionToken, type ConsoleKind } from "@/shared/api";
import { Button } from "@/shared/ui/button";

export function SignOutButton({ kind }: { kind: ConsoleKind }) {
  const token = useSessionToken(kind);
  const queryClient = useQueryClient();

  if (!token) return null;

  return (
    <Button
      variant="ghost"
      size="sm"
      className="w-full justify-start"
      onClick={() => {
        clearToken(kind);
        queryClient.removeQueries({ queryKey: [kind] });
      }}
    >
      <LogOut />
      Выйти
    </Button>
  );
}
