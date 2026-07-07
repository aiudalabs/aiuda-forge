import { createClient } from "@supabase/supabase-js";
import type { Database } from "./database.types";

// The browser holds ONLY the anon key (never service_role). Both come from the
// env, never a literal in the tree — see .env.example.
const url = import.meta.env.VITE_SUPABASE_URL;
const anonKey = import.meta.env.VITE_SUPABASE_ANON_KEY;

if (!url || !anonKey) {
  // Fail loudly in dev; a missing env is a config error, not a silent blank app.
  console.warn(
    "Missing VITE_SUPABASE_URL / VITE_SUPABASE_ANON_KEY — copy .env.example to .env.local.",
  );
}

export const supabase = createClient<Database>(
  url ?? "http://127.0.0.1:54321",
  anonKey ?? "anon-placeholder",
);
