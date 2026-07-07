-- Clean schema: RLS policies + signup trigger all present and correct.

-- profiles: one row per auth user, CREATED BY THE SIGNUP TRIGGER (never seeded).
create table public.profiles (
  id uuid primary key references auth.users (id) on delete cascade,
  email text,
  created_at timestamptz default now()
);
alter table public.profiles enable row level security;
create policy "own profile read" on public.profiles
  for select using (auth.uid() = id);

-- bug #7 guard: signup creates the profile row via a trigger on auth.users.
create function public.handle_new_user() returns trigger
  language plpgsql security definer set search_path = public as $$
begin
  insert into public.profiles (id, email) values (new.id, new.email);
  return new;
end; $$;
create trigger on_auth_user_created
  after insert on auth.users for each row execute function public.handle_new_user();

-- providers: world state (seeded via service_role).
create table public.providers (id uuid primary key, name text, available boolean default true);
alter table public.providers enable row level security;
create policy "providers readable by signed-in" on public.providers
  for select using (auth.uid() is not null);

-- bookings: owner-scoped.
create table public.bookings (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid references auth.users (id),
  provider_id uuid,
  date date
);
alter table public.bookings enable row level security;
-- bug #3 guard: the OWNER can read their own bookings.
create policy "owner reads own bookings" on public.bookings
  for select using (auth.uid() = owner_id);
create policy "owner inserts own bookings" on public.bookings
  for insert with check (auth.uid() = owner_id);
create index bookings_owner_date_idx on public.bookings (owner_id, date desc);

-- admin_secrets: private. NO client select policy => RLS denies every client read.
create table public.admin_secrets (id int primary key, secret text);
alter table public.admin_secrets enable row level security;
