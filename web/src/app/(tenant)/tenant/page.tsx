import { TenantEntryRedirect } from "@/features/console-session";
import { routes } from "@/shared/config/routes";

export default function TenantIndex() {
  return <TenantEntryRedirect to={routes.tenant.hooks} />;
}
