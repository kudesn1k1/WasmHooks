import { PageStub } from "@/shared/ui/page-stub";

export function TenantInvocationsPage() {
  return (
    <PageStub
      title="Лог вызовов"
      milestone="M0"
      description="Вызовы обработчиков тенанта с фильтром по исходу (ok, timeout, handler_error и другие). В PoC обновляется периодическим опросом, в MVP через SSE."
    />
  );
}
