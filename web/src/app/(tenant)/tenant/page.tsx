import { redirect } from "next/navigation";

import { routes } from "@/shared/config/routes";

export default function TenantIndex() {
  redirect(routes.tenant.hooks);
}
