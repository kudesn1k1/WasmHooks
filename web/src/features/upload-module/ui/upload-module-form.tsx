"use client";

import { Upload } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";

import { Button } from "@/shared/ui/button";

import { useUploadModule } from "../api/use-upload-module";
import { checkWasmFile } from "../model/check-wasm";

export function UploadModuleForm({ hook }: { hook: string }) {
  const upload = useUploadModule(hook);
  const inputId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    if (!file) return;
    const problem = await checkWasmFile(file);
    if (problem) {
      setFileError(problem);
      return;
    }
    upload.mutate(file, {
      onSuccess: () => {
        setFile(null);
        if (inputRef.current) inputRef.current.value = "";
      },
    });
  }

  const error = fileError ?? (upload.isError ? upload.error.message : null);

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-2">
      <label htmlFor={inputId} className="text-sm font-medium">
        Новая версия модуля
      </label>
      <div className="flex items-center gap-2">
        <input
          ref={inputRef}
          id={inputId}
          type="file"
          accept=".wasm,application/wasm"
          className="file:bg-secondary file:text-secondary-foreground text-sm file:mr-3 file:rounded-md file:border-0 file:px-3 file:py-1.5 file:text-sm file:font-medium"
          onChange={(e) => {
            setFile(e.target.files?.[0] ?? null);
            setFileError(null);
            upload.reset();
          }}
        />
        <Button type="submit" size="sm" disabled={!file || upload.isPending}>
          <Upload />
          {upload.isPending ? "Загрузка…" : "Загрузить"}
        </Button>
      </div>
      <p className="text-muted-foreground text-xs">
        После загрузки версия проходит проверку платформой. Активной она станет, только когда вы
        её активируете.
      </p>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
    </form>
  );
}
