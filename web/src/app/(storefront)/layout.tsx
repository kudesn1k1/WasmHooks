import type { ReactNode } from "react";

import { StorefrontHeader } from "@/widgets/storefront-header";

export default function StorefrontLayout({ children }: { children: ReactNode }) {
  return (
    <>
      <StorefrontHeader />
      <main className="mx-auto max-w-5xl px-4 py-6">{children}</main>
    </>
  );
}
