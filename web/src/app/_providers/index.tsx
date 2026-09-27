"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { useState, type ReactNode } from "react";

import { MockProvider } from "./mock-provider";

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        retry: 1,
      },
    },
  });
}

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(makeQueryClient);

  return (
    <QueryClientProvider client={queryClient}>
      <MockProvider>{children}</MockProvider>
      <ReactQueryDevtools initialIsOpen={false} />
    </QueryClientProvider>
  );
}
