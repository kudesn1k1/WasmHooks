import { queryOptions } from '@tanstack/react-query';

import { apiRequest } from '@/shared/api';

import {
	customerListSchema,
	productListSchema,
	shopListSchema,
} from '../model/schema';

export const shopsQuery = queryOptions({
	queryKey: ['shop', 'shops'],
	queryFn: ({ signal }) =>
		apiRequest('/shops', {
			schema: shopListSchema,
			service: 'shop',
			signal,
		}),
});

export const customersQuery = queryOptions({
	queryKey: ['shop', 'customers'],
	queryFn: ({ signal }) =>
		apiRequest('/customers', {
			schema: customerListSchema,
			service: 'shop',
			signal,
		}),
});

export function productsQuery(shopId: string) {
	return queryOptions({
		queryKey: ['shop', 'products', shopId],
		queryFn: ({ signal }) =>
			apiRequest(`/shops/${encodeURIComponent(shopId)}/products`, {
				schema: productListSchema,
				service: 'shop',
				signal,
			}),
	});
}
