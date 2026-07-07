-- Buggy schema: the RLS/trigger mistakes that unit tests (which mock the boundary) miss.

-- profiles table exists, but NOTHING creates the row at signup (bug #7: no trigger).
create table public.profiles (
  id uuid primary key references auth.users (id) on delete cascade,
  email text,
  created_at timestamptz default now()
);
alter table public.profiles enable row level security;
create policy "own profile read" on public.profiles
  for select using (auth.uid() = id);
-- (bug #7) NO handle_new_user() trigger on auth.users -> profiles row is never created.

-- providers: world state (seeded via service_role).
create table public.providers (id uuid primary key, name text, available boolean default true);
alter table public.providers enable row level security;
create policy "providers readable by signed-in" on public.providers
  for select using (auth.uid() is not null);

-- bookings: owner-scoped for INSERT, but (bug #3) NO SELECT policy -> RLS denies the
-- owner's own read, so the user sees zero of their own bookings.
create table public.bookings (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid references auth.users (id),
  provider_id uuid,
  date date
);
alter table public.bookings enable row level security;
create policy "owner inserts own bookings" on public.bookings
  for insert with check (auth.uid() = owner_id);
-- (bug #3) missing: a SELECT policy for the owner.
create index bookings_owner_date_idx on public.bookings (owner_id, date desc);

-- admin_secrets: private table with an OVER-PERMISSIVE policy that leaks it to any
-- signed-in client (no_client_over_read violation).
create table public.admin_secrets (id int primary key, secret text);
alter table public.admin_secrets enable row level security;
create policy "oops everyone can read" on public.admin_secrets
  for select using (true);
