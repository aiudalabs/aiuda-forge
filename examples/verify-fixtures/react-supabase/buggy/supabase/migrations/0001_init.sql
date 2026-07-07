create table public.bookings (id uuid primary key, owner_id uuid, date timestamptz);
-- BUG: no index for the (owner_id, date) query.
