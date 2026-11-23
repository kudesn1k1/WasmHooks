'use client';

import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { useState } from 'react';

import {
	MAX_CART_ITEMS,
	MAX_CART_QUANTITY,
	productsQuery,
	shopsQuery,
	type Shop,
} from '@/entities/shop';
import { OpenShopConsoleButton } from '@/features/open-tenant-console';
import { useShopCart } from '@/features/shop-cart';
import { routes } from '@/shared/config/routes';
import { formatPrice } from '@/shared/lib/format';
import { Button } from '@/shared/ui/button';
import {
	Card,
	CardDescription,
	CardFooter,
	CardHeader,
	CardTitle,
} from '@/shared/ui/card';
import { Label } from '@/shared/ui/label';
import { NativeSelect } from '@/shared/ui/native-select';
import { EmptyState, ErrorState, LoadingRows } from '@/shared/ui/query-state';

function ShopProducts({ shop }: { shop: Shop }) {
	const products = useQuery(productsQuery(shop.id));
	const { cart, add } = useShopCart();
	const otherShop = cart && cart.shop.id !== shop.id ? cart.shop : null;

	if (products.isPending) return <LoadingRows />;
	if (products.isError) return <ErrorState error={products.error} />;
	if (!products.data.items.length)
		return <EmptyState>В этом магазине пока нет товаров.</EmptyState>;

	return (
		<>
			{otherShop && (
				<p role='status' className='text-muted-foreground text-sm'>
					В{' '}
					<Link className='underline' href={routes.storefront.cart}>
						корзине
					</Link>{' '}
					уже есть товары магазина «{otherShop.name}». Освободите
					корзину, чтобы добавить товары другого магазина.
				</p>
			)}
			<div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
				{products.data.items.map((product) => {
					const quantity = otherShop
						? 0
						: (cart?.lines.find(
								(line) => line.product.id === product.id,
							)?.quantity ?? 0);
					const limitReached =
						quantity >= MAX_CART_QUANTITY ||
						(quantity === 0 &&
							cart?.lines.length === MAX_CART_ITEMS);

					return (
						<Card key={product.id} className='justify-between'>
							<CardHeader>
								<CardTitle>
									<h2>{product.name}</h2>
								</CardTitle>
								<CardDescription>
									{product.description}
								</CardDescription>
							</CardHeader>
							<CardFooter className='flex-wrap justify-between gap-3'>
								<span className='font-semibold tabular-nums'>
									{formatPrice(product.price)}
								</span>
								<Button
									size='sm'
									aria-label={`В корзину: ${product.name}`}
									disabled={
										Boolean(otherShop) || limitReached
									}
									onClick={() => add(shop, product)}
								>
									{quantity
										? `В корзине: ${quantity}`
										: 'В корзину'}
								</Button>
							</CardFooter>
						</Card>
					);
				})}
			</div>
		</>
	);
}

export function StorefrontCatalogPage() {
	const shops = useQuery(shopsQuery);
	const { cart } = useShopCart();
	const [shopId, setShopId] = useState<string>();
	const shop =
		shops.data?.items.find(
			(item) => item.id === (shopId ?? cart?.shop.id),
		) ?? shops.data?.items[0];

	return (
		<section className='space-y-6'>
			<h1 className='text-2xl font-semibold tracking-tight'>Каталог</h1>
			{shops.isPending && <LoadingRows />}
			{shops.isError && <ErrorState error={shops.error} />}
			{shops.isSuccess && !shop && (
				<EmptyState>Пока нет магазинов.</EmptyState>
			)}
			{shop && (
				<>
					<div className='flex flex-wrap items-end justify-between gap-4'>
						<div className='space-y-2'>
							<Label htmlFor='shop'>Магазин</Label>
							<NativeSelect
								id='shop'
								value={shop.id}
								onChange={(event) =>
									setShopId(event.target.value)
								}
							>
								{shops.data?.items.map((item) => (
									<option key={item.id} value={item.id}>
										{item.name}
									</option>
								))}
							</NativeSelect>
							<p className='text-muted-foreground text-sm'>
								{shop.description}
							</p>
						</div>
						<OpenShopConsoleButton key={shop.id} shopId={shop.id} />
					</div>
					<ShopProducts shop={shop} />
				</>
			)}
		</section>
	);
}
