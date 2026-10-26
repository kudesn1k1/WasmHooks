"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";

import { moduleKeys, type Module } from "@/entities/module";
import { describeError } from "@/shared/api";
import { shortHash } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/shared/ui/dialog";

import { useUploadModule } from "../api/use-upload-module";
import { checkWasmFile } from "../model/check-wasm";
import { isUploadUnconfirmed } from "../model/upload-outcome";

type Props = {
  hook: string;
  activeHash?: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onUploaded?: (module: Module) => void;
};

type Step = "pick" | "picked" | "unconfirmed";

function ErrorText({ children }: { children: string }) {
  return (
    <p role="alert" className="text-destructive text-sm">
      {children}
    </p>
  );
}

export function UploadModuleDialog({ hook, activeHash, open, onOpenChange, onUploaded }: Props) {
  const queryClient = useQueryClient();
  const upload = useUploadModule(hook);
  const inputRef = useRef<HTMLInputElement>(null);
  const [step, setStep] = useState<Step>("pick");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);

  const active = activeHash ? shortHash(activeHash) : null;
  const busy = upload.isPending;

  function reset() {
    setStep("pick");
    setFile(null);
    setError(null);
    upload.reset();
    if (inputRef.current) inputRef.current.value = "";
  }

  function changeOpen(next: boolean) {
    if (busy) return;
    if (!next) reset();
    onOpenChange(next);
  }

  function pickFile() {
    if (inputRef.current) inputRef.current.value = "";
    inputRef.current?.click();
  }

  async function onFileChosen(chosen: File | undefined) {
    if (!chosen) return;
    const problem = await checkWasmFile(chosen);
    setFile(problem ? null : chosen);
    setError(problem);
    setStep(problem ? "pick" : "picked");
  }

  function submit() {
    if (!file) return;
    setError(null);
    upload.mutate(file, {
      onSuccess: (module) => {
        reset();
        onOpenChange(false);
        onUploaded?.(module);
      },
      onError: (e) => {
        if (isUploadUnconfirmed(e)) setStep("unconfirmed");
        else setError(describeError(e));
      },
    });
  }

  function backToVersions() {
    void queryClient.invalidateQueries({ queryKey: moduleKeys.byHook(hook) });
    changeOpen(false);
  }

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent aria-describedby={undefined}>
        <input
          ref={inputRef}
          type="file"
          accept=".wasm,application/wasm"
          aria-label="Файл модуля"
          tabIndex={-1}
          className="sr-only"
          onChange={(e) => void onFileChosen(e.target.files?.[0])}
        />

        {step === "pick" && (
          <>
            <DialogTitle>Загрузить модуль</DialogTitle>
            <DialogDescription asChild>
              <div>
                <p>Хук: {hook}</p>
                <p>
                  Выберите скомпилированный файл .wasm. После загрузки начнётся проверка.
                  {active && ` Текущая версия ${active} продолжит работать.`}
                </p>
              </div>
            </DialogDescription>
            {error && <ErrorText>{error}</ErrorText>}
            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline">Отмена</Button>
              </DialogClose>
              <Button onClick={pickFile}>Выбрать файл</Button>
            </DialogFooter>
          </>
        )}

        {step === "picked" && file && (
          <>
            <DialogTitle>Файл выбран</DialogTitle>
            <DialogDescription asChild>
              <div>
                <p>Хук: {hook}</p>
                <p>Файл: {file.name}</p>
                <p>
                  После загрузки начнётся проверка.
                  {active && ` Версия ${active} продолжит работать до вашей команды активации.`}
                </p>
              </div>
            </DialogDescription>
            {error && <ErrorText>{error}</ErrorText>}
            <DialogFooter>
              <Button variant="outline" disabled={busy} onClick={() => changeOpen(false)}>
                Отмена
              </Button>
              {error ? (
                <Button onClick={pickFile}>Выбрать другой файл</Button>
              ) : (
                <Button disabled={busy} onClick={submit}>
                  {busy ? "Загрузка…" : "Загрузить и проверить"}
                </Button>
              )}
            </DialogFooter>
          </>
        )}

        {step === "unconfirmed" && (
          <>
            <DialogTitle>Не удалось подтвердить загрузку</DialogTitle>
            <DialogDescription asChild>
              <div>
                <p>
                  Ответ сервера не получен. Файл мог загрузиться — проверьте список версий перед
                  повторной попыткой.
                </p>
                <p>Активная версия не изменилась.</p>
              </div>
            </DialogDescription>
            <DialogFooter>
              <Button onClick={backToVersions}>К версиям</Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
