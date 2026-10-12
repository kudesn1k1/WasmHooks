"use client";

import { KeyRound } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode } from "react";

import { ApiError, describeError, setToken, useSessionToken, type ConsoleKind } from "@/shared/api";
import { Button } from "@/shared/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/shared/ui/card";

type Props = {
  kind: ConsoleKind;
  title: string;
  description: string;
  label: string;
  verify?: (token: string) => Promise<unknown>;
  rejectedMessage?: string;
  hint?: ReactNode;
  children: ReactNode;
};

export function SessionGate({
  kind,
  title,
  description,
  label,
  verify,
  rejectedMessage = "Токен не подошёл",
  hint,
  children,
}: Props) {
  const token = useSessionToken(kind);
  const inputId = useId();
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);

  if (token === undefined) return null;
  if (token) return children;

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const candidate = value.trim();
    if (!candidate) return;
    setError(null);
    if (verify) {
      setChecking(true);
      try {
        await verify(candidate);
      } catch (e) {
        setError(e instanceof ApiError && e.status === 401 ? rejectedMessage : describeError(e));
        return;
      } finally {
        setChecking(false);
      }
    }
    setToken(kind, candidate);
  }

  return (
    <Card className="max-w-lg">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <form onSubmit={onSubmit} className="flex flex-col gap-3">
          <label htmlFor={inputId} className="text-sm font-medium">
            {label}
          </label>
          <input
            id={inputId}
            type="password"
            autoComplete="off"
            value={value}
            aria-invalid={error ? true : undefined}
            onChange={(e) => {
              setValue(e.target.value);
              setError(null);
            }}
            className="border-input focus-visible:ring-ring/50 aria-invalid:border-destructive h-9 rounded-md border bg-transparent px-3 font-mono text-sm outline-none focus-visible:ring-[3px]"
          />
          {error && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          <Button type="submit" disabled={!value.trim() || checking} className="w-fit">
            <KeyRound />
            {checking ? "Проверка…" : "Войти"}
          </Button>
        </form>
        {hint}
      </CardContent>
    </Card>
  );
}
