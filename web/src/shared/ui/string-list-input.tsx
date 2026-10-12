"use client";

import { Lock, Plus, X } from "lucide-react";
import { useState, type KeyboardEvent } from "react";

import { Badge } from "@/shared/ui/badge";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";

type Props = {
  id?: string;
  value: string[];
  onChange: (value: string[]) => void;
  locked?: string[];
  placeholder?: string;
  lockedHint?: string;
};

export function StringListInput({ id, value, onChange, locked = [], placeholder, lockedHint }: Props) {
  const [draft, setDraft] = useState("");
  const candidate = draft.trim();

  function add() {
    if (!candidate || value.includes(candidate)) return;
    onChange([...value, candidate]);
    setDraft("");
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== "Enter") return;
    event.preventDefault();
    add();
  }

  return (
    <div className="flex flex-col gap-2">
      {value.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Добавленные значения">
          {value.map((item) => {
            const isLocked = locked.includes(item);
            return (
              <li key={item}>
                <Badge variant="secondary" className="gap-1 py-1 pr-1 font-mono">
                  {item}
                  {isLocked ? (
                    <span role="img" title={lockedHint} aria-label={lockedHint ?? "Удалить нельзя"}>
                      <Lock className="text-muted-foreground size-3" />
                    </span>
                  ) : (
                    <button
                      type="button"
                      aria-label={`Убрать ${item}`}
                      className="hover:bg-background rounded-sm p-0.5"
                      onClick={() => onChange(value.filter((v) => v !== item))}
                    >
                      <X className="size-3" />
                    </button>
                  )}
                </Badge>
              </li>
            );
          })}
        </ul>
      )}
      <div className="flex gap-2">
        <Input
          id={id}
          value={draft}
          placeholder={placeholder}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKeyDown}
          className="font-mono"
        />
        <Button type="button" variant="outline" disabled={!candidate || value.includes(candidate)} onClick={add}>
          <Plus />
          Добавить
        </Button>
      </div>
    </div>
  );
}
