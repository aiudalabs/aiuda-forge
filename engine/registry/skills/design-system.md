# Skill — design-system

How to define a REAL, opinionated visual design system for a product — the thing that
makes its UI look intentional and distinctive instead of generic AI-template output.

> NOTE: The operative content of this skill is inlined into the `designer` persona (and
> the design-system phase), because the runtime injects only the persona — skill files
> are not loaded at runtime. Keep them in sync. This file is the canonical reference.

## Why this exists

AI fills missing design intent with averages → generic "SaaS template" output. The cure
is to COMMIT to one concrete aesthetic direction and specify it precisely. A design that
only says "clean / modern / minimal" has no direction — those are taste words, not design
decisions. Your job is to make the decisions, concretely, up front.

## The five essentials (ALL required — drop one and it goes generic)

1. **Reference + emotion.** Name the aesthetic and 1–2 real references ("like Linear",
   "like Stripe", "editorial magazine", "warm marketplace like Airbnb"), and the emotion
   the product should evoke (trustworthy, energetic, calm, premium, playful). Commit to
   ONE direction — never blend three.
2. **Color palette.** A committed palette with real hex values: a dominant brand color +
   a sharp accent (NOT a timid, evenly-distributed set), neutrals, and semantic roles.
3. **Typography.** Named, distinctive typefaces (a display/heading face + a body face),
   a type scale, and weight contrast.
4. **Spacing rhythm.** A spacing scale, radii, and elevation/shadow scale.
5. **Shape & motion.** Corner language (sharp vs rounded), border/shadow feel, and where
   motion is used (high-impact moments, not scattered).

## Aesthetic rules (from Anthropic's frontend-aesthetics guidance)

- **Typography**: choose beautiful, distinctive fonts. **AVOID overused ones — Inter,
  Roboto, Arial, system-ui.** Prefer faces with character: Space Grotesk, Satoshi, Clash
  Display, Cabinet Grotesk, Sora, DM Sans, Fraunces, Instrument Serif. Use high-contrast
  pairings (display + mono, serif + geometric sans) and real weight contrast (300 vs 800),
  not 400 vs 600.
- **Color**: commit to a cohesive palette via CSS variables. **Dominant colors with sharp
  accents beat timid, evenly-distributed palettes.** Draw from IDE themes, cultural and
  domain aesthetics. AVOID clichés — especially purple-gradient-on-white.
- **Backgrounds / depth**: create atmosphere — layer subtle gradients, mesh/radial
  gradients, soft geometric texture or contextual effects. AVOID flat solid-white defaults
  with no intentionality.
- **Layout / components**: give the design context-specific character. AVOID predictable,
  cookie-cutter layouts and identical card styling everywhere. Distinctive cards, hover
  states, considered detailing.
- **Motion**: reserve animation for high-impact moments (one orchestrated page-load with
  staggered reveals), not scattered micro-interactions.

The system tends to converge toward generic output — push deliberately the other way.

## Token architecture (Material Design 3 — single source of truth)

Three tiers, so screens reference roles (not raw hex), and theming is one place:

1. **Reference tokens** — the raw scale: a tonal palette derived from each key color
   (brand, accent, neutral) at multiple tones; the type families + scale; spacing; radii;
   shadows; breakpoints.
2. **Semantic tokens** — intent-named roles mapped to reference tones: `surface`,
   `on-surface`, `surface-variant`, `text`, `text-muted`, `border`, `primary`,
   `on-primary`, `accent`, `success`, `warning`, `danger`. Screens use THESE.
3. **Component tokens** — per-component values (button height/radius/states, card
   elevation, input border) built from the semantic tokens.

Color pairings must meet **WCAG 2.1 AA** contrast (text on its surface, on-primary on
primary). Provide a reduced-motion fallback.

## Output: `docs/DESIGN_SYSTEM.md`

Write a versioned, reusable spec the UX/mockups/dev all consume. Structure:

```markdown
# Design System — <Product>

## Direction
- Aesthetic: <named direction> · References: <1–2 real refs>
- Emotion: <trustworthy / energetic / …> · Audience: <who>
- Principles: <3–5 one-liners that guide every choice>

## Color (reference → semantic)
- Brand key: #RRGGBB → tonal ramp (50…900)
- Accent key: #RRGGBB → ramp
- Neutrals: warm/cool gray ramp
- Semantic: surface #… / on-surface #… / text #… / text-muted #… / border #… /
  primary #… / on-primary #… / accent #… / success #… / warning #… / danger #…
- Contrast: <list the AA-verified pairs>

## Typography
- Display/headings: "<Font Name>" (weights …) — <why it fits>
- Body/UI: "<Font Name>" (weights …)
- Type scale: <h1…body…caption sizes/line-heights>

## Spacing, radii, elevation, shape
- Spacing scale: 4/8-based … · Radii: … · Shadows/elevation: … · Corner language: …

## Motion
- Where + how (durations, easing, the page-load choreography)

## Components (built from the tokens)
- Button (variants + states) · Card · Input/Field · Badge · empty/loading/error
```

Also emit the machine-readable tokens (`docs/design-tokens.css` `:root{ --… }` and/or
`design-tokens.json`) so the dev agents wire them into the theme (Tailwind/shadcn,
Flutter ThemeData) instead of inventing per-screen values.

## Concrete example (a committed direction — model your output on this density)

> Product: a local services marketplace. Direction: **"Warm trust"** — references Airbnb
> (marketplace warmth) + Stripe (clean restraint). Emotion: trustworthy + approachable.
>
> - **Type**: Display "Clash Display" (600/700); Body "DM Sans" (400/500). High contrast,
>   no Inter/system.
> - **Color**: brand teal `#0F766E` (trust) → ramp; accent warm coral `#F97316` (action);
>   warm neutral ramp `#FAFAF9 / #E7E5E4 / #78716C / #1C1917`; success `#16A34A`, danger
>   `#DC2626`. Semantic: surface `#FFFFFF`, app-bg `#FAFAF9`, text `#1C1917`, primary
>   `#0F766E`/on-primary `#FFFFFF`, accent `#F97316`. All AA-verified.
> - **Spacing**: 4px base. Radii 12px. Soft elevation (`0 1px 2px / 0 8px 24px` at depth).
>   Corner language: rounded, friendly. Background: subtle warm radial wash, not flat white.
> - **Motion**: one staggered card reveal on the homepage; hover lift on provider cards.
> - **Components**: provider Card features photo + rating prominently; primary Button is
>   coral, full states; inputs with clear focus ring in brand teal.

This is the level of commitment to produce — not "a clean neutral palette with one accent".
