import { createClient } from "@supabase/supabase-js";
// Uses the service_role key (bypasses RLS).
const admin = createClient(Deno.env.get("SUPABASE_URL")!, Deno.env.get("SERVICE_ROLE")!);
export default async () => new Response("ok");
