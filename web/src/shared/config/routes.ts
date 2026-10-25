export const routes = {
  storefront: {
    catalog: "/",
    cart: "/cart",
  },
  tenant: {
    root: "/tenant",
    hooks: "/tenant/hooks",
    hook: (name: string) => `/tenant/hooks/${encodeURIComponent(name)}`,
    invocations: "/tenant/invocations",
  },
  operator: {
    root: "/operator",
    hooks: "/operator/hooks",
    newHook: "/operator/hooks/new-hook",
    hook: (name: string) => `/operator/hooks/${encodeURIComponent(name)}`,
    tenants: "/operator/tenants",
    apiKeys: "/operator/api-keys",
  },
} as const;
