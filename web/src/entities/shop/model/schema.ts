import { z } from 'zod';

const money = z.number().int().nonnegative();
const shopSchema = z.object({
	id: z.string(),
	name: z.string(),
	description: z.string(),
});

const productSchema = shopSchema.extend({
	shop_id: z.string(),
	price: money,
});

const customerSchema = z.object({
	id: z.string(),
	name: z.string(),
	lifetime_spend: money,
});

export const MAX_CART_QUANTITY = 99;
export const MAX_CART_ITEMS = 50;

const cartItemSchema = z.object({
	product_id: z.string(),
	quantity: z.number().int().min(1).max(MAX_CART_QUANTITY),
});

export const shopCartSchema = z.object({
	shop: shopSchema,
	lines: z.array(z.object({
		product: productSchema,
		quantity: cartItemSchema.shape.quantity,
	})).min(1).max(MAX_CART_ITEMS),
}).refine(({ shop, lines }) =>
	lines.every(({ product }) => product.shop_id === shop.id)
	&& new Set(lines.map(({ product }) => product.id)).size === lines.length,
);

export const checkoutRequestSchema = z.strictObject({
	customer_id: z.string(),
	items: z.array(cartItemSchema.strict()).min(1).max(MAX_CART_ITEMS),
});

export const shopListSchema = z.object({ items: z.array(shopSchema) });
export const productListSchema = z.object({ items: z.array(productSchema) });
export const customerListSchema = z.object({ items: z.array(customerSchema) });

export const checkoutSchema = z.object({
	shop_id: z.string(),
	customer_id: z.string(),
	lines: z.array(
		cartItemSchema.extend({
			name: z.string(),
			unit_price: money,
			line_total: money,
			quantity: z.number().int().positive(),
		}),
	),
	subtotal: money,
	discount: z.object({
		percent: z.number().int().min(0).max(100),
		amount: money,
		reason: z.string().nullable(),
		outcome: z.string(),
		fallback: z.boolean(),
	}),
	total: money,
});

export type Shop = z.infer<typeof shopSchema>;
export type ShopCart = z.infer<typeof shopCartSchema>;
export type Product = z.infer<typeof productSchema>;
export type Checkout = z.infer<typeof checkoutSchema>;
export type CheckoutRequest = z.infer<typeof checkoutRequestSchema>;
