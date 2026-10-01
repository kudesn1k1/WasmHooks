import type { Metadata } from "next";

import { TenantHookPage } from "@/views/tenant-hook";

type Props = { params: Promise<{ hook: string }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { hook } = await params;
  return { title: decodeURIComponent(hook) };
}

export default async function Page({ params }: Props) {
  const { hook } = await params;
  return <TenantHookPage hook={decodeURIComponent(hook)} />;
}
