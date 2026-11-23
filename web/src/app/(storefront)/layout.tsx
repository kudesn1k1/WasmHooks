import type { ReactNode } from 'react';

import { ShopCartProvider } from '@/features/shop-cart';
import { StorefrontHeader } from '@/widgets/storefront-header';

export default function StorefrontLayout({
	children,
}: {
	children: ReactNode;
}) {
	return (
		<ShopCartProvider>
			<div className='bg-muted/40 min-h-screen'>
				<StorefrontHeader />
				<main className='mx-auto max-w-[83rem] px-4 py-6 sm:px-6 sm:py-7 lg:px-10'>
					{children}
				</main>
			</div>
		</ShopCartProvider>
	);
}
