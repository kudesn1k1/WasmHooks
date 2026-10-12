"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { adoptTokenFromFragment } from "../model/tenant-token";

export function TenantEntryRedirect({ to }: { to: string }) {
  const router = useRouter();

  useEffect(() => {
    adoptTokenFromFragment();
    router.replace(to);
  }, [router, to]);

  return null;
}
