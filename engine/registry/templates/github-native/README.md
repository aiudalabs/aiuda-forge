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
        └── claude-review.yml                  # cross-model reviewer on every PR
```

`_common/` holds stack-agnostic files (the roster shell and the two QA
workflows). Each `<stack>/` folder holds the stack-specific personas, per-path
instructions, toolchain and constitution. A generated repo = `_common/` + one
`<stack>/`.

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
5. Add `CLAUDE.md.tmpl` (repo constitution) referencing the same lanes, path map
   and validation commands.
6. Reuse `_common/` unchanged.
