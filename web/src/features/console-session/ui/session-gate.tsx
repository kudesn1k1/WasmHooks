"use client";

import { KeyRound } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";

import { setToken, useSessionToken, type ConsoleKind } from "@/shared/api";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/shared/ui/card";

type Props = {
  kind: ConsoleKind;
  title: string;
  description: string;
  label: string;
  children: ReactNode;
};

export function SessionGate({ kind, title, description, label, children }: Props) {
  const token = useSessionToken(kind);
  const inputId = useId();
  const [value, setValue] = useState("");

  if (token === undefined) return null;
  if (token) return children;

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = value.trim();
    if (trimmed) setToken(kind, trimmed);
  }

  return (
    <Card className="max-w-lg">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={onSubmit} className="flex flex-col gap-3">
          <label htmlFor={inputId} className="text-sm font-medium">
            {label}
          </label>
          <input
            id={inputId}
            type="password"
            autoComplete="off"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="border-input focus-visible:ring-ring/50 h-9 rounded-md border bg-transparent px-3 font-mono text-sm outline-none focus-visible:ring-[3px]"
          />
          <Button type="submit" disabled={!value.trim()} className="w-fit">
            <KeyRound />
            Войти
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
