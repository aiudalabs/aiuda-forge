# react-supabase demo

Minimal buildable reference app for the **`react-supabase`** stack profile
(`engine/registry/templates/github-native/react-supabase/`). It exists to prove
S0 of the multi-stack verification plan
(`docs/PLAN-2026-07-07-verificacion-e2e-multiplataforma.md` §7): *a react-supabase
project scaffolds and builds* — before any verification layer is added.

## Layout (matches the stack's declared layout)

```
/                     Vite React app root (react-dev lane)
  index.html
  vite.config.ts
  src/**              components, pages, hooks
    lib/supabase.ts        singleton typed client (anon key only)
    lib/database.types.ts  generated types — supabase-dev owns, read-only here
  supabase/**         backend (supabase-dev lane)
    config.toml
    migrations/**     SQL: tables, constraints, RLS policies
```

## Build

```bash
npm install
npm run build      # tsc -b && vite build  → dist/
```

The app boots without a live backend (the `todos` query errors offline and the UI
shows its error state), which is exactly what `ui-verify` needs to smoke the home.

## Run against a live backend (optional)

```bash
supabase start                 # boots Postgres + Auth locally, applies migrations
cp .env.example .env.local     # fill VITE_SUPABASE_URL / VITE_SUPABASE_ANON_KEY
npm run dev
```

## What this is NOT

Not the verification layer. `provisioning-lint`, `e2e-verify`, and `ui-fidelity`
arrive in later sprints (S2+). This is only the scaffold + a buildable app.

## Scaffolding the agent config onto a real repo

The `.github/` agent config, `CLAUDE.md`, and `AGENTS.md` are **not** committed
here (they would drift from the templates). To overlay them on a real project's
repo, call `POST /projects/{id}/scaffold/github` with `{"stack":"react-supabase"}`
— it renders them from the registry templates.
