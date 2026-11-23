import type { ComponentProps } from 'react';

import { cn } from '@/shared/lib/utils';

export function NativeSelect({
	className,
	...props
}: ComponentProps<'select'>) {
	return (
		<select
			className={cn(
				'border-input bg-background focus-visible:ring-ring h-9 w-full rounded-md border px-3 text-sm shadow-xs focus-visible:ring-2 focus-visible:outline-none disabled:opacity-50',
				className,
			)}
			{...props}
		/>
	);
}
