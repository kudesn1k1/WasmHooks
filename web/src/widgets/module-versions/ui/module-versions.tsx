"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import type { Binding } from "@/entities/hook";
import {
  describeCheckFailure,
  firstFailedCheck,
  isPending,
  moduleQueries,
  moduleStatusLabels,
  type Module,
} from "@/entities/module";
import { ActivateModuleDialog } from "@/features/activate-module";
import { UploadModuleDialog } from "@/features/upload-module";
import { shortHash } from "@/shared/lib/format";
import { Button } from "@/shared/ui/button";
import { InfoPanel } from "@/shared/ui/info-panel";
import { ErrorState, LoadingRows } from "@/shared/ui/query-state";

type Props = {
  hook: string;
  binding?: Binding | null;
};

type Focus = { kind: "activated" | "rejected"; id: string } | null;

function newestFirst(modules: Module[]): Module[] {
  return modules.toSorted((a, b) => b.uploaded_at.localeCompare(a.uploaded_at));
}

function RejectedPanel({ module, onUpload }: { module: Module; onUpload: () => void }) {
  const failed = firstFailedCheck(module);
  const failure = failed && describeCheckFailure(failed);
  return (
    <InfoPanel
      role="status"
      title={failure ? `Модуль отклонён: ${failure.title}` : "Модуль отклонён"}
      action={
        <Button variant="outline" onClick={onUpload}>
          Загрузить исправленный файл
        </Button>
      }
    >
      <p className="font-mono text-xs break-all">
        {shortHash(module.content_hash)}
        {failed?.detail && `: ${failed.detail}`}
      </p>
      {failure && <p>{failure.hint}</p>}
      <p>Активная версия не изменилась.</p>
    </InfoPanel>
  );
}

type PanelState =
  | { kind: "saved" | "checking" | "ready" | "rejected"; module: Module }
  | { kind: "idle" };

function panelState(modules: Module[], activeId: string | null | undefined, focus: Focus): PanelState {
  const latest = modules[0];
  const focused = focus && modules.find((m) => m.id === focus.id);
  if (focus?.kind === "activated" && focused && focused.id === activeId) return { kind: "saved", module: focused };
  if (focus?.kind === "rejected" && focused) return { kind: "rejected", module: focused };
  if (!latest) return { kind: "idle" };
  if (isPending(latest.status)) return { kind: "checking", module: latest };
  if (latest.status === "rejected") return { kind: "rejected", module: latest };
  if (latest.status === "validated" && latest.id !== activeId) return { kind: "ready", module: latest };
  return { kind: "idle" };
}

function StatusPanel({ state, onUpload }: { state: PanelState; onUpload: () => void }) {
  switch (state.kind) {
    case "saved":
      return (
        <InfoPanel role="status" title="Активная версия сохранена">
          <p>
            Выбрана версия {shortHash(state.module.content_hash)}. Обновление исполнителей может занять
            некоторое время. Активация не запускает обработчик.
          </p>
        </InfoPanel>
      );
    case "checking":
      return (
        <InfoPanel role="status" title={`Проверяем модуль ${shortHash(state.module.content_hash)}`}>
          <p>
            Проверяем модуль и выполняем пробный вызов. Активировать можно после успешной проверки.
            Текущая активная версия не меняется.
          </p>
        </InfoPanel>
      );
    case "rejected":
      return <RejectedPanel module={state.module} onUpload={onUpload} />;
    case "ready":
      return (
        <InfoPanel role="status" title="Модуль готов к активации">
          <p>
            Проверка пройдена на примере входа. Это не гарантирует работу на всех данных. Версия ещё не
            активна.
          </p>
        </InfoPanel>
      );
    case "idle":
      return (
        <InfoPanel title="Новая версия не включается автоматически">
          <p>После загрузки модуль проходит проверку. Вы сами выбираете момент активации.</p>
        </InfoPanel>
      );
  }
}

export function ModuleVersions({ hook, binding }: Props) {
  const { data, error, isPending: loading } = useQuery(moduleQueries.byHook(hook));
  const [uploadOpen, setUploadOpen] = useState(false);
  const [focus, setFocus] = useState<Focus>(null);

  const activeId = binding?.active_module_id;
  const activeHash = binding?.active_module_hash;
  const modules = data ? newestFirst(data) : [];
  const panel = panelState(modules, activeId, focus);
  const explainedId = panel.kind === "rejected" ? panel.module.id : undefined;

  function action(module: Module) {
    if (module.id === activeId) return "–";
    if (module.status === "validated") {
      return (
        <ActivateModuleDialog
          hook={hook}
          module={module}
          activeHash={activeHash}
          onActivated={() => setFocus({ kind: "activated", id: module.id })}
        />
      );
    }
    if (module.status === "rejected") {
      return module.id === explainedId ? (
        "Причина ниже"
      ) : (
        <Button variant="link" className="h-auto p-0" onClick={() => setFocus({ kind: "rejected", id: module.id })}>
          Показать причину
        </Button>
      );
    }
    return "–";
  }

  return (
    <>
      <section className="flex flex-col gap-4 rounded-md border px-6 py-6">
        <div className="flex items-center justify-between gap-4">
          <h2 className="text-lg font-semibold">Версии модуля</h2>
          <Button onClick={() => setUploadOpen(true)}>Загрузить модуль</Button>
        </div>

        {loading ? (
          <LoadingRows />
        ) : error ? (
          <ErrorState error={error} />
        ) : modules.length === 0 ? (
          <p className="text-muted-foreground py-4 text-sm">Версий пока нет. Загрузите первый .wasm-модуль.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="bg-muted text-muted-foreground text-left text-xs">
                  <th className="h-9 w-1/4 font-normal">Версия (хеш)</th>
                  <th className="h-9 w-1/4 font-normal">Проверка</th>
                  <th className="h-9 w-1/5 font-normal">Использование</th>
                  <th className="h-9 font-normal">Действие</th>
                </tr>
              </thead>
              <tbody>
                {modules.map((module) => (
                  <tr key={module.id}>
                    <td className="h-17" title={module.content_hash}>
                      {shortHash(module.content_hash)}
                    </td>
                    <td>{moduleStatusLabels[module.status]}</td>
                    <td>{module.id === activeId ? "Активна" : "Не активна"}</td>
                    <td>{action(module)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {data && (
        <StatusPanel state={panel} onUpload={() => setUploadOpen(true)} />
      )}

      <UploadModuleDialog
        hook={hook}
        activeHash={activeHash}
        open={uploadOpen}
        onOpenChange={setUploadOpen}
        onUploaded={() => setFocus(null)}
      />
    </>
  );
}
