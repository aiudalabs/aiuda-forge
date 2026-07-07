import { supabase } from "./lib/supabase";
export async function myBookings(uid: string) {
  const { data } = await supabase
    .from("bookings")
    .select("id, date")
    .eq("owner_id", uid)
    .order("date", { ascending: false });
  return data;
}
