const SESSION_TTL_MS = 60 * 60 * 1000;

function base64Url(value: unknown): string {
	const bytes = new TextEncoder().encode(JSON.stringify(value));
	return btoa(String.fromCharCode(...bytes))
		.replace(/\+/g, '-')
		.replace(/\//g, '_')
		.replace(/=+$/, '');
}

export function mockConsoleSession(externalId: string) {
	const now = Date.now();
	const expiresAt = new Date(now + SESSION_TTL_MS);
	const claims = {
		sub: externalId,
		aud: 'tenant-console',
		iss: 'wasmhooks-control-plane',
		iat: Math.floor(now / 1000),
		exp: Math.floor(expiresAt.getTime() / 1000),
	};
	return {
		token: `${base64Url({ alg: 'HS256', typ: 'JWT' })}.${base64Url(claims)}.mock-signature`,
		expires_at: expiresAt.toISOString(),
	};
}
