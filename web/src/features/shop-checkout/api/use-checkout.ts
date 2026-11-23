import { useMutation } from '@tanstack/react-query';

import { checkoutSchema, type CheckoutRequest } from '@/entities/shop';
import { apiRequest } from '@/shared/api';

export function useCheckout(shopId: string) {
	return useMutation({
		mutationFn: (body: CheckoutRequest) =>
			apiRequest(`/shops/${encodeURIComponent(shopId)}/checkout`, {
				method: 'POST',
				schema: checkoutSchema,
				service: 'shop',
				body,
			}),
	});
}
