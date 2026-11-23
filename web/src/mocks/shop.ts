import { delay, http, HttpResponse } from 'msw';

import {
	checkoutRequestSchema,
	type Checkout,
	type Product,
	type Shop,
} from '@/entities/shop';
import { SHOP_API_BASE_URL } from '@/shared/config/env';

import { mockConsoleSession } from './console-session';
import { problem } from './http';

export const shopUrl = (path: string) => `*${SHOP_API_BASE_URL}${path}`;

const shops: Shop[] = [
	{
		id: 'merchant-a',
		name: 'Чайная лавка',
		description: 'Чай, посуда и мёд.',
	},
	{
		id: 'merchant-b',
		name: 'Кофейня на углу',
		description: 'Кофе в зёрнах и всё для варки.',
	},
];

const products: Product[] = [
	{
		id: 'tea-sencha',
		shop_id: 'merchant-a',
		name: 'Чай сенча',
		description: 'Зелёный японский чай, 100 г.',
		price: 45000,
	},
	{
		id: 'teapot-glass',
		shop_id: 'merchant-a',
		name: 'Стеклянный чайник',
		description: 'Жаропрочное стекло, 600 мл.',
		price: 210000,
	},
	{
		id: 'honey-linden',
		shop_id: 'merchant-a',
		name: 'Липовый мёд',
		description: 'Алтайский мёд, 500 г.',
		price: 65000,
	},
	{
		id: 'coffee-ethiopia',
		shop_id: 'merchant-b',
		name: 'Кофе Эфиопия',
		description: 'Зерно средней обжарки, 250 г.',
		price: 89000,
	},
	{
		id: 'cezve-copper',
		shop_id: 'merchant-b',
		name: 'Медная турка',
		description: 'Ручная чеканка, 300 мл.',
		price: 180000,
	},
	{
		id: 'mug-ceramic',
		shop_id: 'merchant-b',
		name: 'Керамическая кружка',
		description: 'Ручная работа, 350 мл.',
		price: 120000,
	},
];

const customers = [
	{ id: 'c-new', name: 'Новый покупатель', lifetime_spend: 0 },
	{ id: 'c-occasional', name: 'Случайный покупатель', lifetime_spend: 80000 },
	{ id: 'c-loyal', name: 'Постоянный покупатель', lifetime_spend: 240000 },
];

export const shopHandlers = [
	http.get(shopUrl('/shops'), async () => {
		await delay();
		return HttpResponse.json({ items: shops });
	}),
	http.get(shopUrl('/shops/:shopId/products'), async ({ params }) => {
		await delay();
		if (!shops.some((shop) => shop.id === params.shopId))
			return problem(404, 'Магазин не найден');
		return HttpResponse.json({
			items: products.filter(
				(product) => product.shop_id === params.shopId,
			),
		});
	}),
	http.get(shopUrl('/customers'), async () => {
		await delay();
		return HttpResponse.json({ items: customers });
	}),
	http.post(
		shopUrl('/shops/:shopId/checkout'),
		async ({ params, request }) => {
			await delay();
			const shop = shops.find((item) => item.id === params.shopId);
			if (!shop) return problem(404, 'Магазин не найден');
			const body = checkoutRequestSchema.safeParse(
				await request.json().catch(() => undefined),
			);
			if (!body.success) return problem(422, 'Некорректный запрос');

			const customer = customers.find(
				(item) => item.id === body.data.customer_id,
			);
			if (!customer) return problem(404, 'Покупатель не найден');

			const lines: Checkout['lines'] = [];
			for (const { product_id, quantity } of body.data.items) {
				const product = products.find(
					(item) =>
						item.id === product_id && item.shop_id === shop.id,
				);
				if (!product)
					return problem(404, 'Товар не найден', product_id);
				lines.push({
					product_id,
					quantity,
					name: product.name,
					unit_price: product.price,
					line_total: product.price * quantity,
				});
			}

			const subtotal = lines.reduce(
				(sum, line) => sum + line.line_total,
				0,
			);
			const fallback = shop.id !== 'merchant-a';
			const percent =
				!fallback && customer.lifetime_spend >= 100000 ? 10 : 0;
			const amount = Math.floor((subtotal * percent) / 100);
			return HttpResponse.json({
				shop_id: shop.id,
				customer_id: customer.id,
				lines,
				subtotal,
				total: subtotal - amount,
				discount: {
					percent,
					amount,
					fallback,
					outcome: fallback ? 'no_handler' : 'ok',
					reason: fallback
						? null
						: percent
							? 'Скидка постоянного покупателя'
							: 'Порог прошлых покупок не достигнут',
				},
			} satisfies Checkout);
		},
	),
	http.post(
		shopUrl('/shops/:shopId/console-sessions'),
		async ({ params }) => {
			await delay();
			const shop = shops.find((item) => item.id === params.shopId);
			if (!shop) return problem(404, 'Магазин не найден');
			return HttpResponse.json(mockConsoleSession(shop.id), {
				status: 201,
			});
		},
	),
];
