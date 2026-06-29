# Design System Overhaul — arreglar la blandura estética en la RAÍZ

Estado: **plan para aprobación** (2026-06-28). Objetivo: que TODO proyecto que pase
por Studio salga con una UI con dirección estética real, no genérica.

## Context — por qué
La fábrica produce UIs sosas **por diseño**: ningún paso de Studio es dueño de una
dirección estética, y el agente que sí toca estilo (`designer`) está instruido a hacer
justo lo que la guía de aesthetics de Anthropic dice EVITAR.

Diagnóstico (mapeado en `registry/agents/` + `registry/workflows/design.yaml`):
- **`designer.md`** ordena: *system fonts, sin web fonts · paleta neutra + 1 acento ·
  gradientes solo si aportan señal · layout 1040 centrado*. Anthropic dice lo contrario:
  *fuentes distintivas (evita Inter/Roboto/Arial/system) · color dominante + acento fuerte
  · fondos con profundidad · carácter contextual, una sola dirección*.
- **`ux.md`** (UI_SCREENS.md) = solo estructura/comportamiento ("no implementation
  detail") → cero color/tipografía.
- Los tokens del `designer` viven **inline en un HTML desechable**, no documentados ni
  reutilizables.
- **`story-detailer.md`** dice leer "design tokens en UI_SCREENS.md" → **referencia rota**
  (nadie los escribe). Dev agents (react/flutter) esperan tokens del theme → no existen.
- No hay `DESIGN_SYSTEM.md` ni tokens versionados. Brief/PRD/Arquitectura: cero marca.

Fuentes: [Claude frontend aesthetics](https://github.com/anthropics/claude-cookbooks/blob/main/coding/prompting_for_frontend_aesthetics.ipynb)
· [Improving frontend design through Skills](https://claude.com/blog/improving-frontend-design-through-skills)
· [Material Design 3 tokens](https://m3.material.io/foundations/design-tokens/overview)
· [por qué la UI con IA sale genérica](https://gendesigns.ai/blog/ai-generated-ui-mistakes-how-to-fix)

## Principios (de la investigación)
1. **Intención antes que estética**: capturar referencia + emoción + audiencia; "clean/
   modern" no es dirección. Comprometerse con UNA dirección, sin mezclar.
2. **Las 5 esenciales, concretas**: referencia de marca · paleta (hex) · tipografía (con
   nombre) · ritmo de espaciado · emoción objetivo. Sin una → genérico.
3. **Reglas de Anthropic**: fuentes distintivas (no Inter/system) · color dominante +
   acento · fondos con profundidad · carácter contextual · motion en momentos clave.
4. **Tokens MD3**: 3 niveles (referencia → semántico → componente), una sola fuente de
   verdad, pares de color accesibles (AA), shape + elevation + type scale.

## El artefacto nuevo: `docs/DESIGN_SYSTEM.md` (+ `docs/design-tokens.css|json`)
Producido ANTES de pantallas/mockups, versionado, reutilizable. Contiene:
- **Dirección**: personalidad, emoción, referencia comprometida, audiencia.
- **Tokens referencia**: paleta tonal (hue clave → tonos), tipografía (display+cuerpo
  con nombres reales), escala de espaciado, radios, sombras/elevación, breakpoints.
- **Tokens semánticos** (intención): surface/text/border/primary/accent/success/danger…
- **Reglas de componente**: botones (variantes+estados), cards, inputs, badges, estados
  loading/empty/error — descritos con los tokens, no con hex sueltos.
- **A11y**: contraste AA verificado, focus visible, motion reducido.

## Plan por fases (suite verde + commit por paso; PR por fase)

### Fase 0 — Skill `design-system` (la base reutilizable)
- Nuevo skill en `registry/skills/design-system` con: las reglas de Anthropic + MD3, las
  5 esenciales, y la PLANTILLA de `DESIGN_SYSTEM.md` (con ejemplo concreto). Es el "cómo"
  inyectable, análogo al brand-guidelines skill de Anthropic.
- (Opcional) presets de dirección (p. ej. "warm-trust", "minimal-premium", "editorial")
  que un proyecto puede elegir/override.

### Fase 1 — Reescribir el agente `designer` (alto impacto, aislado)
- Adoptar las reglas de Anthropic; **quitar** los constraints que garantizan blandura
  (system-fonts-only, 1-acento, layout-molde). El mockup sigue self-contained pero PUEDE
  usar Google Fonts (`@import`) y paleta real.
- El designer ahora **consume** el `DESIGN_SYSTEM.md` (no reinventa tokens) y los
  **escribe** al repo como artefacto (no solo inline en el HTML).
- Validar con 1 proyecto de prueba (eyeball del mockup) antes de seguir.

### Fase 2 — Fase "Design System" en `design.yaml` + intención temprana
- Nuevo paso `design_system` (agente fortalecido o `designer`) que produce
  `DESIGN_SYSTEM.md` **antes** de `ui`/`mockups`, usando el skill de Fase 0.
- Capturar **intención estética** en discovery/PRD: sección de marca/dirección (el humano
  la da; si no, el agente propone una apropiada al dominio y se compromete).

### Fase 3 — Cablearlo de punta a punta
- `ux.md`: referenciar el DESIGN_SYSTEM (las pantallas usan roles semánticos).
- `scrum-master`: incluir una story "design-system foundation" (wave 1) que materializa
  los tokens; las stories de UI dependen de ella.
- `story-detailer` + dev agents (react/flutter): leer `DESIGN_SYSTEM.md` (arreglar la
  referencia rota a UI_SCREENS.md).

### Fase 4 — Guardarraíles
- NFR de "Sistema visual" en el PRD; mecanismo de diseño en arquitectura.
- Check del `product-advisor`: falla si no hay dirección estética / tokens.
- (Opcional) gate de diseño que rechaza un backlog UI sin design-system.

### Fase 5 — Aplicar a serviciospty (validación end-to-end)
- Re-correr la design-system story de serviciospty con el nuevo enfoque y comparar el
  antes/después. Si el mockup/tokens ya se ven bien → seguir con las superficies.

## Verificación
- Correr `design` (o `iterate`) sobre un proyecto de muestra → inspeccionar
  `DESIGN_SYSTEM.md`: ¿tiene fuentes con nombre, paleta hex, dirección comprometida?
- Construir 1 pantalla con esos tokens y **mirarla** (Playwright screenshot) — ¿se ve
  distintiva, no genérica?
- Suite del engine verde (los agentes son datos del registry; los cambios de workflow se
  validan con los tests de parsing/handoff existentes).

## Decisiones (confirmadas)
- **Humano-en-el-loop estético**: ✅ **El agente propone + gate humano.** El agente de
  design-system propone una dirección apropiada al dominio (referencia, paleta, fuentes,
  emoción) y se COMPROMETE; el humano la revisa/ajusta en el gate antes de construir.
- **Base de tokens**: MD3 (referencia→semántico→componente). ✅
- **Presets de marca**: opcionales, después de Fase 0 (no bloquean el plan base).

## Fuera de alcance
Generación de logos/assets de marca; theming multi-marca por tenant; modo oscuro
avanzado (se puede sumar luego sobre los tokens semánticos).
