import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ShopCartProvider } from '@/features/shop-cart';
import { problem } from '@/mocks/http';
import { server } from '@/mocks/node';
import { shopUrl } from '@/mocks/shop';
import { getToken } from '@/shared/api';
import { renderWithQuery } from '@/shared/lib/test-utils';
import { StorefrontCartPage } from '@/views/storefront-cart';

import { StorefrontCatalogPage } from './storefront-catalog-page';

function renderStorefront() {
	return renderWithQuery(
		<ShopCartProvider>
			<StorefrontCatalogPage />
			<StorefrontCartPage />
		</ShopCartProvider>,
	);
}

describe('storefront', () => {
	afterEach(() => vi.restoreAllMocks());

	it('restores the shop and quantities after a page reload and persists clearing the cart', async () => {
		const user = userEvent.setup();
		const storefront = renderStorefront();
		await screen.findByRole('option', { name: 'Кофейня на углу' });
		await user.selectOptions(screen.getByLabelText('Магазин'), 'merchant-b');
		const coffee = await screen.findByRole('button', { name: 'В корзину: Кофе Эфиопия' });
		await user.click(coffee);
		await user.click(coffee);
		await user.click(screen.getByRole('button', { name: 'В корзину: Медная турка' }));
		storefront.unmount();

		const renderCart = () => renderWithQuery(
			<ShopCartProvider><StorefrontCartPage /></ShopCartProvider>,
		);
		const cart = renderCart();
		expect(screen.getByRole('heading', { name: 'Кофейня на углу' })).toBeInTheDocument();
		expect(screen.getByText(/^3\s580,00\s₽$/)).toBeInTheDocument();
		await user.click(screen.getByRole('button', { name: 'Очистить корзину' }));
		expect(screen.getByText('Корзина пуста.')).toBeInTheDocument();
		cart.unmount();

		renderCart();
		expect(screen.getByText('Корзина пуста.')).toBeInTheDocument();
	});

	it('adds products, changes quantities and calculates a discount for the selected buyer', async () => {
		const user = userEvent.setup();
		renderStorefront();
		expect(screen.getByText('Корзина пуста.')).toBeInTheDocument();

		await user.click(
			await screen.findByRole('button', { name: 'В корзину: Чай сенча' }),
		);
		await user.click(
			screen.getByRole('button', {
				name: 'Увеличить количество: Чай сенча',
			}),
		);
		await screen.findByRole('option', { name: 'Постоянный покупатель' });
		await user.selectOptions(
			screen.getByLabelText('Покупатель'),
			'c-loyal',
		);
		await user.click(
			screen.getByRole('button', { name: 'Рассчитать заказ' }),
		);

		const result = within(await screen.findByRole('status'));
		expect(result.getByText('Скидка (10%)')).toBeInTheDocument();
		expect(result.getByText(/^900,00\s₽$/)).toBeInTheDocument();
		expect(result.getByText(/^90,00\s₽$/)).toBeInTheDocument();
		expect(result.getByText(/^810,00\s₽$/)).toBeInTheDocument();

		await user.click(
			screen.getByRole('button', {
				name: 'Уменьшить количество: Чай сенча',
			}),
		);
		expect(screen.queryByText('Скидка (10%)')).not.toBeInTheDocument();
		expect(
			screen.getByRole('button', {
				name: 'Уменьшить количество: Чай сенча',
			}),
		).toBeDisabled();
	});

	it('keeps the basket tied to one shop until its last line is removed', async () => {
		const user = userEvent.setup();
		renderStorefront();
		await user.click(
			await screen.findByRole('button', { name: 'В корзину: Чай сенча' }),
		);
		await user.selectOptions(
			screen.getByLabelText('Магазин'),
			'merchant-b',
		);

		const coffee = await screen.findByRole('button', {
			name: 'В корзину: Кофе Эфиопия',
		});
		expect(coffee).toBeDisabled();
		expect(screen.getByRole('status')).toHaveTextContent('Чайная лавка');
		await user.click(
			screen.getByRole('button', { name: 'Удалить: Чай сенча' }),
		);
		expect(coffee).toBeEnabled();
		await user.click(coffee);
		expect(
			screen.getByRole('heading', { name: 'Кофейня на углу' }),
		).toBeInTheDocument();
	});

	it('shows the total and a zero discount when the hook times out', async () => {
		const user = userEvent.setup();
		server.use(
			http.post(shopUrl('/shops/merchant-a/checkout'), () =>
				HttpResponse.json({
					shop_id: 'merchant-a',
					customer_id: 'c-new',
					lines: [
						{
							product_id: 'tea-sencha',
							name: 'Чай сенча',
							unit_price: 45000,
							quantity: 1,
							line_total: 45000,
						},
					],
					subtotal: 45000,
					discount: {
						percent: 0,
						amount: 0,
						fallback: true,
						reason: null,
						outcome: 'timeout',
					},
					total: 45000,
				}),
			),
		);
		renderStorefront();

		await user.click(
			await screen.findByRole('button', { name: 'В корзину: Чай сенча' }),
		);
		await screen.findByRole('option', { name: 'Новый покупатель' });
		await user.click(
			screen.getByRole('button', { name: 'Рассчитать заказ' }),
		);

		const result = within(await screen.findByRole('status'));
		expect(result.getByText('Скидка (0%)')).toBeInTheDocument();
		expect(result.getByText('timeout')).toBeInTheDocument();
		expect(result.getAllByText(/^450,00\s₽$/)).toHaveLength(2);
		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
	});

	it('opens the selected merchant console without sending the installation key', async () => {
		const user = userEvent.setup();
		const tab = {
			opener: window,
			sessionStorage: { clear: vi.fn() },
			location: { href: '' },
			close: vi.fn(),
		};
		vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window);
		const authorization = vi.fn();
		server.use(
			http.post(
				shopUrl('/shops/merchant-b/console-sessions'),
				({ request }) => {
					authorization(request.headers.get('Authorization'));
					return HttpResponse.json(
						{
							token: 'demo+token',
							expires_at: '2026-11-23T16:00:00Z',
						},
						{ status: 201 },
					);
				},
			),
		);
		renderStorefront();

		await screen.findByRole('option', { name: 'Кофейня на углу' });
		await user.selectOptions(
			screen.getByLabelText('Магазин'),
			'merchant-b',
		);
		await user.click(screen.getByRole('button', { name: 'Мои скрипты' }));

		await waitFor(() =>
			expect(tab.location.href).toBe(
				`${window.location.origin}/tenant#token=demo%2Btoken`,
			),
		);
		expect(authorization).toHaveBeenCalledWith(null);
		expect(tab.opener).toBeNull();
		expect(tab.sessionStorage.clear).toHaveBeenCalledOnce();
	});

	it('shows an API error without clearing either console session and allows retrying', async () => {
		const user = userEvent.setup();
		server.use(
			http.post(
				shopUrl('/shops/merchant-a/checkout'),
				() => problem(401, 'Чекаут недоступен'),
				{ once: true },
			),
		);
		renderStorefront();

		await user.click(
			await screen.findByRole('button', { name: 'В корзину: Чай сенча' }),
		);
		await screen.findByRole('option', { name: 'Новый покупатель' });
		await user.click(
			screen.getByRole('button', { name: 'Рассчитать заказ' }),
		);

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Чекаут недоступен',
		);
		expect(getToken('operator')).toBe('whk_test');
		expect(getToken('tenant')).toBe('tenant-test-token');
		await user.click(
			screen.getByRole('button', { name: 'Рассчитать заказ' }),
		);
		expect(await screen.findByRole('status')).toHaveTextContent(
			'Скидка (0%)',
		);
		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
	});
});
