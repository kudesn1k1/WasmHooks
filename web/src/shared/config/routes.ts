// Все пути приложения в одном месте: страницы и навигация ссылаются сюда,
// а не на строковые литералы.
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
    tenants: "/operator/tenants",
  },
} as const;
