import { PageStub } from "@/shared/ui/page-stub";

export function TenantHookPage({ hook }: { hook: string }) {
  return (
    <PageStub
      title={hook}
      milestone="M0"
      description="Версии модулей на хуке со статусами (uploaded, validating, validated, rejected, archived), загрузка .wasm, активация и откат. Активна не версия, а привязка «тенант, хук» к ней."
    />
  );
}
