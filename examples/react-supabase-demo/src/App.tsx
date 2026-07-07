import { useQuery } from "@tanstack/react-query";
import { supabase } from "./lib/supabase";

// Minimal demo screen: reads `todos` through the typed Supabase client wrapped in
// TanStack Query, and implements the loading / error / empty / populated states
// the stack's frontend rules require. It renders without a live backend (the
// query simply errors offline) so the app boots green for ui-verify.
export default function App() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ["todos"],
    queryFn: async () => {
      const { data, error } = await supabase
        .from("todos")
        .select("id, title, done")
        .order("created_at", { ascending: false });
      if (error) throw error;
      return data;
    },
    retry: false,
  });

  return (
    <main style={{ fontFamily: "system-ui", padding: "2rem", maxWidth: 640 }}>
      <h1>react-supabase demo</h1>
      <p>
        Reference app for the <code>react-supabase</code> stack: Vite + React +
        TypeScript talking to Supabase through a typed client and TanStack Query.
      </p>
      <section aria-labelledby="todos-heading">
        <h2 id="todos-heading">Todos</h2>
        {isLoading && <p>Loading…</p>}
        {isError && (
          <p role="alert">
            No backend connected — run <code>supabase start</code> and set
            <code> .env.local</code> to see live data.
          </p>
        )}
        {!isLoading && !isError && (data?.length ?? 0) === 0 && (
          <p>No todos yet.</p>
        )}
        {!isLoading && !isError && (data?.length ?? 0) > 0 && (
          <ul>
            {data!.map((t) => (
              <li key={t.id}>
                {t.done ? "✓" : "○"} {t.title}
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
