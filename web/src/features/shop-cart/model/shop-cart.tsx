'use client';

import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';

import {
	MAX_CART_ITEMS,
	MAX_CART_QUANTITY,
	shopCartSchema,
	type Product,
	type Shop,
	type ShopCart as Cart,
} from '@/entities/shop';

const STORAGE_KEY = 'wasmhooks.shop.cart';

function readCart(): Cart | null {
	try {
		const saved = sessionStorage.getItem(STORAGE_KEY);
		return saved ? shopCartSchema.safeParse(JSON.parse(saved)).data ?? null : null;
	} catch {
		return null;
	}
}

function writeCart(cart: Cart | null) {
	try {
		if (cart) sessionStorage.setItem(STORAGE_KEY, JSON.stringify(cart));
		else sessionStorage.removeItem(STORAGE_KEY);
	} catch {
		// The current cart still works if session storage is unavailable.
	}
}

function addProduct(cart: Cart | null, shop: Shop, product: Product): Cart {
	if (!cart) return { shop, lines: [{ product, quantity: 1 }] };
	if (cart.shop.id !== shop.id) return cart;

	const line = cart.lines.find((item) => item.product.id === product.id);
	if (!line) {
		if (cart.lines.length >= MAX_CART_ITEMS) return cart;
		return { ...cart, lines: [...cart.lines, { product, quantity: 1 }] };
	}
	if (line.quantity >= MAX_CART_QUANTITY) return cart;
	return {
		...cart,
		lines: cart.lines.map((item) =>
			item === line ? { ...item, quantity: item.quantity + 1 } : item,
		),
	};
}

type ShopCart = {
	cart: Cart | null;
	isRestoring: boolean;
	add: (shop: Shop, product: Product) => void;
	setQuantity: (productId: string, quantity: number) => void;
	clear: () => void;
};

const CartContext = createContext<ShopCart | null>(null);

export function ShopCartProvider({ children }: { children: ReactNode }) {
	const [cart, setCart] = useState<Cart | null>();

	useEffect(() => {
		setCart(readCart());
	}, []);

	useEffect(() => {
		if (cart !== undefined) writeCart(cart);
	}, [cart]);

	function setQuantity(productId: string, quantity: number) {
		setCart((current) => {
			if (
				!current ||
				!Number.isInteger(quantity) ||
				quantity < 0 ||
				quantity > MAX_CART_QUANTITY
			) {
				return current;
			}
			const lines =
				quantity === 0
					? current.lines.filter(
							({ product }) => product.id !== productId,
						)
					: current.lines.map((line) =>
							line.product.id === productId
								? { ...line, quantity }
								: line,
						);
			return lines.length ? { ...current, lines } : null;
		});
	}

	return (
		<CartContext.Provider
			value={{
				cart: cart ?? null,
				isRestoring: cart === undefined,
				add: (shop, product) =>
					setCart((current) => addProduct(current ?? null, shop, product)),
				setQuantity,
				clear: () => setCart(null),
			}}
		>
			{children}
		</CartContext.Provider>
	);
}

export function useShopCart() {
	const value = useContext(CartContext);
	if (!value) throw new Error('ShopCartProvider is missing');
	return value;
}
