import { InvocationLog } from "@/widgets/invocation-log";

export function TenantInvocationsPage() {
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Лог вызовов</h1>
        <p className="text-muted-foreground text-sm">
          Последние вызовы ваших обработчиков. Наведите на строку, чтобы увидеть логи скрипта.
        </p>
      </div>
      <InvocationLog />
    </div>
  );
}
