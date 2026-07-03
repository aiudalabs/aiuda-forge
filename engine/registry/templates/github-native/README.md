# GitHub-native templates

These templates bake the engineering specialization that used to be injected at
runtime (from `engine/registry/agents/*.md`) into files that ship **inside every
generated repo**. After the GitHub-native pivot the actual development happens in
GitHub (Copilot cloud agent / Claude / `claude-code-action`), so the personas can
no longer be injected by our kernel at step time — they travel with the repo.

## What the Studio generates

When a design is published, the Studio scaffolds the client's repo and renders
these `.tmpl` files (simple string substitution) into their final paths:

```
<generated repo>/
├── AGENTS.md                                  # tool-neutral roster + path ownership + validation
├── CLAUDE.md                                  # repo constitution (from the stack template)
└── .github/
    ├── agents/<lane>.agent.md                 # per-lane persona (GitHub custom agent)
    ├── instructions/<area>.instructions.md    # per-path rules (applyTo globs)
    └── workflows/
        ├── copilot-setup-steps.yml            # toolchain the Copilot agent needs
        ├── suite-integrity.yml                # anti "delete-the-failing-test" guard
        ├── claude-review.yml                  # cross-model reviewer on every PR
        └── ui-verify.yml                      # functional UI gate (Playwright smoke)
```

`_common/` holds stack-agnostic files (the roster shell and the two QA
workflows). Each `<stack>/` folder holds the stack-specific personas, per-path
instructions, toolchain, constitution and `ui-verify.yml` (the functional UI
gate is per-stack because how you build+serve the app differs: Vite/Next preview
for `python-fastapi-react`, `flutter build web` for `aiuda-flutter-firebase`). A
generated repo = `_common/` + one `<stack>/`.

### ui-verify.yml — the functional UI gate (Wave V3)

`suite-integrity` and `claude-review` make "the tests pass" credible and judge
the diff; `ui-verify` goes one step further and checks the app **actually boots
and renders** in a real headless browser — a much stronger signal than green
unit tests. It runs `on: pull_request` only when the PR touches frontend paths
(`frontend/**` for python-fastapi-react; `apps/**`/`packages/**` for the Flutter
stack), builds and serves the app, waits for the port, then runs an inline
Playwright smoke that has **no dependency on tests in the repo**: it navigates to
the home, asserts a 200, asserts the page is not blank (body text OR a render
surface — a `#root`/`#app`/`main` with children for SPAs, or a `canvas`/
`flt-glass-pane`/`flutter-view` for Flutter web), fails on any uncaught JS or
console error, and uploads a full-page screenshot as an artifact. The check FAILS
if the app does not start or the home does not render — it is a gate, not
decoration.

## Template variables

Substitution is a plain string replace — no logic, no conditionals. The design
pipeline already produces every value:

| Variable | Meaning | Source |
|---|---|---|
| `{{project_name}}` | Human product name | `PRODUCT_BRIEF.md` (Phase 1) |
| `{{stack}}` | Stack profile id (`python-fastapi-react`, `aiuda-flutter-firebase`) | locked in `OPINIONATED_DEFAULTS.md` |
| `{{lanes}}` | Pre-rendered bullet list of lanes (name — one-line scope) | `AGENTS.md`/architecture |
| `{{design_tokens}}` | Pre-rendered token block (palette, type, spacing, radii, shadows) | `DESIGN_SYSTEM.md` (Phase 4) |
| `{{path_map_backend}}` | Pre-rendered path-ownership block for the backend lane | `ARCHITECTURE.md` |
| `{{path_map_frontend}}` | Pre-rendered path-ownership block for the frontend lane | `ARCHITECTURE.md` |
| `{{validation_commands}}` | Pre-rendered list of the exact lint/typecheck/test commands CI runs | stack profile / `ARCHITECTURE.md` |
| `{{language}}` | Primary human language for microcopy/UX (e.g. `es`, `en`) | `OPINIONATED_DEFAULTS.md` |
| `{{app_path}}` | Path of the primary app that `ui-verify.yml` builds+serves (Flutter stack only; default `apps/customer`) | `ARCHITECTURE.md` |

Values that expand to multiple lines (`{{lanes}}`, `{{design_tokens}}`,
`{{path_map_*}}`, `{{validation_commands}}`) are rendered by the generator as
markdown fragments and dropped in verbatim; the templates place them where a
list or block belongs.

## How the personas relate to the runtime originals

The `.agent.md.tmpl` files are ports of `engine/registry/agents/{python-dev,
react-dev,flutter-dev,firebase-dev}.md`. The port **keeps** the engineering
judgment (read-spec-first, minimal change, error-paths before happy-path, ≤2
nesting levels, honest tests per acceptance criterion, stay-in-lane, apply the
design tokens) and **drops** the kernel mechanics that no longer exist in a
GitHub-native repo:

- the whole `.vibeforge-gate` / anti-tamper / "never edit the gate" doctrine —
  the gate is now GitHub Actions running `{{validation_commands}}`;
- vendor-deps-offline (`.venv` committed into the tree, `node_modules` surviving
  the container) — CI installs deps from lockfiles via `copilot-setup-steps.yml`;
- "do NOT commit / push / open a PR" — this is **inverted**: the agent now
  commits its work on a branch and opens the PR itself;
- the `$step.output.text` / "your final reply IS the ticket" runner contract and
  any reference to the kernel workdir/clone.

The two skills that used to be concatenated at runtime are **inlined** at the end
of the relevant persona: `acceptance-self-audit` in every dev agent, and
`frontend-quality` additionally in the React and Flutter agents. The `reviewer`
persona is ported into `_common/.github/workflows/claude-review.yml` as the
cross-model PR reviewer prompt.

## Adding a stack

1. Create `engine/registry/templates/github-native/<new-stack>/`.
2. Add one `.github/agents/<lane>.agent.md.tmpl` per lane (port the engineering
   judgment; do not reintroduce gate/vendor/no-commit doctrine).
3. Add `.github/instructions/<area>.instructions.md.tmpl` per path group with an
   `applyTo:` glob matching that lane's files.
4. Add `.github/workflows/copilot-setup-steps.yml.tmpl` installing that stack's
   toolchain.
5. Add `.github/workflows/ui-verify.yml.tmpl` if the stack has a UI: build+serve
   the app for that stack, then reuse the same generic Playwright smoke (it
   already handles both SPA and canvas-based frontends). Gate it on the stack's
   frontend paths and guard each step with `hashFiles(...)`.
6. Add `CLAUDE.md.tmpl` (repo constitution) referencing the same lanes, path map
   and validation commands.
7. Reuse `_common/` unchanged.
