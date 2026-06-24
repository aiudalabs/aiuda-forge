"use client";

// Providers de la app: TanStack Query (estado de servidor) + suscripción al bus WS (tiempo real).

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { useRealtime } from "@/lib/hooks";

function Realtime() {
  useRealtime();
  return null;
}

export function Providers({ children }: { children: React.ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { staleTime: 4000, refetchOnWindowFocus: false, retry: 1 },
        },
      }),
  );

  return (
    <QueryClientProvider client={client}>
      <Realtime />
      {children}
    </QueryClientProvider>
  );
}
