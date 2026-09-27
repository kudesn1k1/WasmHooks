import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { renderWithQuery } from '@/shared/lib/test-utils';

import { InvocationLog } from './invocation-log';

const outcomesInTable = () =>
	within(screen.getByRole('table'))
		.getAllByRole('row')
		.slice(1)
		.map((r) => within(r).getAllByRole('cell')[2].textContent);

describe('InvocationLog', () => {
	it('filters invocations by outcome', async () => {
		const user = userEvent.setup();
		renderWithQuery(<InvocationLog />);

		await screen.findByRole('table');
		expect(outcomesInTable().some((t) => t?.startsWith('ok'))).toBe(true);

		await user.click(screen.getByRole('button', { name: 'timeout' }));

		await waitFor(() => {
			const cells = outcomesInTable();
			expect(cells.length).toBeGreaterThan(0);
			expect(cells.every((t) => t?.startsWith('timeout'))).toBe(true);
		});
		expect(screen.getByRole('button', { name: 'timeout' })).toHaveAttribute(
			'aria-pressed',
			'true',
		);
	});
});
