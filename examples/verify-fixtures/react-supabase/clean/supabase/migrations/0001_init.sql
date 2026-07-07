create table public.bookings (id uuid primary key, owner_id uuid, date timestamptz);
create index bookings_owner_date_idx on public.bookings (owner_id, date desc);
