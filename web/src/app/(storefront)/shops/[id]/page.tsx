import { StorefrontShopPage } from "@/views/storefront-shop";

export default async function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <StorefrontShopPage shopId={id} />;
}
