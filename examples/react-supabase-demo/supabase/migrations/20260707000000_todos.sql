-- Demo migration: the `todos` table with Row-Level Security. Shows the stack's
-- backend contract — a table ships with its constraints, its indexes, and its RLS
-- policies in the same migration, and server-owned columns are set server-side.

create table if not exists public.todos (
  id         uuid primary key default gen_random_uuid(),
  owner_id   uuid not null references auth.users (id) on delete cascade,
  title      text not null check (char_length(title) between 1 and 200),
  done       boolean not null default false,
  created_at timestamptz not null default now()
);

create index if not exists todos_owner_created_idx
  on public.todos (owner_id, created_at desc);

-- owner_id is server-owned: default it from the authenticated user so the client
-- cannot forge another user's row.
alter table public.todos
  alter column owner_id set default auth.uid();

-- RLS is the security boundary. Enabled + explicit per-command policies scoped to
-- the authenticated owner; a client can only ever see and mutate its own rows.
alter table public.todos enable row level security;

create policy "todos_select_own" on public.todos
  for select to authenticated
  using (owner_id = auth.uid());

create policy "todos_insert_own" on public.todos
  for insert to authenticated
  with check (owner_id = auth.uid());

create policy "todos_update_own" on public.todos
  for update to authenticated
  using (owner_id = auth.uid())
  with check (owner_id = auth.uid());

create policy "todos_delete_own" on public.todos
  for delete to authenticated
  using (owner_id = auth.uid());
