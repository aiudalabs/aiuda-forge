# Skill — mockup-html

How to produce a stakeholder-ready, self-contained HTML prototype.

> NOTE: The operative content of this skill is also inlined into the `designer`
> persona (`registry/agents/designer.md`), because the runtime injects only the
> persona into the agent — skill files are not loaded at runtime. Keep the two in
> sync when editing. This file is the human-readable reference.

## Core rule: one file, zero dependencies

The output is a **single HTML file** with all CSS and JavaScript written inline
(inside `<style>` and `<script>` tags). No `<link>` to external stylesheets, no
`<script src="...">`, no external images. The file must render correctly with no
internet connection.

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
  --bg:#f7f8fa; --surface:#fff; --text:#1a1d21; --muted:#6b7280;
  --accent:#<one domain-appropriate hue>; --accent-ink:#fff;
  --ok:#16a34a; --warn:#d97706; --danger:#dc2626; --border:#e5e7eb;
  --radius:10px; --gap:16px; --shadow:0 1px 3px rgba(0,0,0,.08);
}
```

## Visual design guidelines

- **Palette**: use a clean neutral base (white / light grey) with one accent colour
  derived from the product's domain. No gradients unless they add clear signal.
- **Typography**: system font stack — `system-ui, -apple-system, sans-serif`.
  Base size 15–16 px, headings via `em` multiples.
- **Spacing**: 8 px grid. All margins/padding multiples of 4 px.
- **Components to include (as needed)**:
  - Cards / list rows with realistic data fields
  - Buttons (primary, secondary, destructive) with hover states
  - Form inputs — text, select, checkbox — with labels
  - Status badges / chips
  - Empty states (icon + message + CTA)
  - Loading shimmer (optional — a simple animated gradient strip)
- **Responsive**: at minimum, use `max-width: 960px; margin: auto` so the mockup
  does not stretch on wide screens. Mobile breakpoint is optional for stakeholder review.

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

- External fonts (Google Fonts, Typekit, etc.)
- Icon libraries (use Unicode symbols or simple SVG inline shapes instead)
- CSS frameworks loaded from CDN (Bootstrap, Tailwind CDN build)
- Placeholder images from external services (picsum, lorempixel)
  — use inline SVG or coloured `<div>` placeholders instead
- Alert boxes or `console.log` calls in the final file
