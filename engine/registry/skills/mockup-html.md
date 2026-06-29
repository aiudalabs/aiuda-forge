# Skill — mockup-html

How to produce a stakeholder-ready, self-contained HTML prototype.

> NOTE: The operative content of this skill is also inlined into the `designer`
> persona (`registry/agents/designer.md`), because the runtime injects only the
> persona into the agent — skill files are not loaded at runtime. Keep the two in
> sync when editing. This file is the human-readable reference.

## Core rule: one file, web fonts allowed

The output is a **single HTML file** with all CSS and JavaScript written inline (inside
`<style>` and `<script>` tags). The ONE allowed external resource is **web fonts** (Google
Fonts `<link>`/`@import`) — real type is what stops it looking like a wireframe. No other
external stylesheets, no `<script src>`, no external images.

## File structure

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>[Product Name] — Mockup</title>
  <style>
    /* All styles here — reset, layout, components, utilities */
  </style>
</head>
<body>
  <!-- Top navigation bar with screen links -->
  <nav>…</nav>

  <!-- One <section id="screen-name"> per screen; only one visible at a time -->
  <section id="screen-dashboard" class="screen active">…</section>
  <section id="screen-detail" class="screen">…</section>

  <script>
    /* Screen switching logic only — no framework, plain DOM */
  </script>
</body>
</html>
```

## Navigation

Provide a top bar listing every screen by its short name. Clicking a name:
1. Removes the `active` class from all `.screen` elements.
2. Adds `active` to the target screen.
3. Updates the active link style.

Keep the navigation logic in ~10 lines of vanilla JS.

## Design tokens

Define a small token set once in `:root` and reuse it everywhere — never hardcode a colour
per element. This makes the mockup look like one coherent product:

```css
:root{
  /* semantic roles from the committed palette — fill with real, characterful hex */
  --app-bg:#…; --surface:#…; --text:#…; --text-muted:#…; --border:#…;
  --primary:#…; --on-primary:#…; --accent:#…; --on-accent:#…;
  --success:#16a34a; --warning:#d97706; --danger:#dc2626;
  --radius:…; --gap:…; --shadow-sm:…; --shadow-lg:…;
  --font-display:"…"; --font-body:"…";
}
```

## Visual design — see the `design-system` skill

The VISUAL direction (palette, typography, depth, shape, motion) is governed by the
`design-system` skill, not by safe defaults. Do NOT default to "neutral base + one accent +
system fonts" — that is exactly the generic look to avoid. Commit to a concrete aesthetic:
distinctive web fonts (not Inter/system), a dominant color + sharp accent, backgrounds with
depth, and context-specific layout. Use semantic CSS-variable tokens so the mockup looks
like one coherent, intentional product.

- **Components to include (as needed)**: cards / list rows with realistic data fields;
  buttons (primary / secondary / ghost / destructive) with hover + focus states; form
  inputs with labels and a visible focus ring; status badges / chips; empty + loading +
  error states.
- **Layout**: the structure the product's flow wants (app shell, grid, editorial…),
  responsive, no horizontal scroll — not a one-size `max-width` box for every product.

## Realistic content

Every label, name, date, and number must feel plausible:
- User names: real-sounding (e.g. "María García", "James Okafor") — not "User A"
- Dates: relative to today, formatted for the product's locale
- Numbers: in the right order of magnitude for the domain
- Status values: drawn from the actual domain vocabulary in the PRD/UI spec

## Screens per flow

Cover the primary happy-path flow end to end. Typical set:
1. Landing / Dashboard
2. List / Board view of core objects
3. Detail view of a single object
4. Create / Edit form
5. Confirmation or success state

Add a screen only if it materially changes stakeholder understanding.
Do not add screens just to hit a count.

## Avoid

- Generic typography — system fonts, Inter, Roboto, Arial (Google Fonts web fonts ARE
  encouraged; pick a distinctive face per the `design-system` skill)
- Icon libraries (use Unicode symbols or simple SVG inline shapes instead)
- CSS frameworks loaded from CDN (Bootstrap, Tailwind CDN build)
- Placeholder images from external services (picsum, lorempixel)
  — use inline SVG or coloured `<div>` placeholders instead
- Alert boxes or `console.log` calls in the final file
