import type { Metadata } from "next";

import { OperatorHookPage } from "@/views/operator-hook";

type Props = { params: Promise<{ name: string }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { name } = await params;
  return { title: decodeURIComponent(name) };
}

export default async function Page({ params }: Props) {
  const { name } = await params;
  return <OperatorHookPage name={decodeURIComponent(name)} />;
}
