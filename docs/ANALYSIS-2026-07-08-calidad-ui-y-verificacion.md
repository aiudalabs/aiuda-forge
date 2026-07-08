# Análisis profundo — por qué la UI sale mediocre + estado real de la verificación E2E

Investigación con evidencia (archivo:línea + salidas `gh`) sobre dos preguntas que resultan ser
**la misma falla desde dos lados**: (1) por qué la UI generada por Fluxo es mediocre cuando
Claude Code a mano haría algo mejor, y (2) si la verificación con emuladores/Playwright llegó a
implementarse y correr de verdad.

---

## Parte 1 — Por qué la UI sale "de principiante"

### Reencuadre honesto (importante para el fix)
La premisa es **medio cierta**. Lo verificado:
- La app **no es "de principiante"** — es **profesional-pero-genérica-e-inconsistente**. ~80%
  construida desde el design system "Aura", con pipeline de imágenes, transiciones shared-element,
  disciplina de reduce-motion. (Ojo: este repo tuvo además un "redesign premium" a mano que
  *favorece* al pipeline.)
- Los artefactos de diseño **son buenos de verdad**: la persona `designer`
  (`engine/registry/agents/designer.md`) produce un mockup **premium** (`docs/mockups/index.html`:
  3 familias tipográficas, sombras tokenizadas de 3 niveles, gradientes atmosféricos, motion
  escalonado, shell glassmórfico). `DESIGN_SYSTEM.md` y `UI_SCREENS.md` son inusualmente
  prescriptivos (rampas hex exactas, contraste AA, inventario de 24 componentes, estados por pantalla).

**La degradación NO está en la intención de diseño ni en la spec. Ocurre en la COSTURA entre la
fase de diseño y la fase de construcción** — convierte una spec premium en un build competente-
pero-genérico.

### Los mecanismos de degradación (ordenados por impacto)

1. **El mockup —el artefacto visual más rico— NUNCA se le muestra al agente que construye.**
   El prompt de dispatch es pelado: `dispatch.go:433-443` → *"Resolve issue #N. Implement EXACTLY
   what the AC specify — every checkbox, nothing more."* Sin mockup, sin screen spec. El issue body
   (`export.go:195-225`) = story + ACs + module map. Sin link al mockup. El `screen_key` que el
   scrum-master emite en cada story frontend (`scrum-master.md:100-106`) **no lo consume nadie**
   (grep = 0 consumidores): la fase de binding no existe → `screen_key` es un no-op latente.

2. **El contrato de build es "minimalismo hacia ACs funcionales" — la calidad visual NO está en la
   función de recompensa.** `dispatch.go:440` "nothing more"; `flutter-dev.agent.md:13,34,44`
   "precisely and minimally", "smallest change", "do not add…". Y los ACs son **funcionales, no
   visuales** (`scrum-master.md:115`: "outcomes observables", ej. "catálogo vacío muestra empty-state").
   Nada premia jerarquía, imágenes, pulido o deleite. El agente optimiza *pasar*, no *impresionar*.

3. **El paso de enriquecimiento por-story (`story-detailer`) se PERDIÓ en el pivote GitHub-native.**
   El scrum-master manda a propósito un **skeleton LIGERO** (1-3 líneas + 2-5 ACs) difiriendo el
   spec dev-ready al story-detailer "at build time" (`scrum-master.md:108-114`, `design.yaml:199`).
   Pero `story-detailer` corre **solo en `factory.yaml:14`** (el factory legacy, OFF por default).
   El `design.yaml` vigente NO tiene ese paso → el conductor despacha contra el issue ligero,
   sub-especificado *por diseño*, **menos el paso que iba a compensarlo**.

4. **Fragmentación por-story → cero consistencia entre pantallas (el "look de IA").** Cada story es
   un GitHub Action efímero y aislado; el único contexto cross-story es un **module map** (nombres de
   dirs + conteos, `dispatch.go:511-536`), nunca los componentes hermanos. Resultado, del código real:
   `_DayStrip` **duplicado literal** en dos pantallas; slots con `_SlotChip` hand-rolled en una vs
   `AuraChip` en otra; **dos lenguajes de loading** (`AuraSkeletonList` vs `CircularProgressIndicator`);
   **dos sistemas de notificación** (`AuraToast` vs `ScaffoldMessenger.showSnackBar`). Huellas
   exactas de agentes que nunca ven el trabajo del otro.

5. **La verificación valida "arranca", nunca "se ve bien" — no hay director de arte.** `ui-verify`
   pasa con HTTP 200 + body no-vacío + sin errores JS; **saca screenshot y lo sube como artifact
   (`:147,:165-171`) pero nada lo JUZGA.** `claude-review`: lo visual es WARNING/INFO → APPROVE
   (non-blocking), y hasta le dicen que "gold-plating… is the failure mode". `suite-integrity`: solo
   cuenta tests. **El gate entero es ciego a la estética.**

6. **Los design tokens nunca llegan al agente.** `app.instructions.md.tmpl:16` tiene "## Design
   tokens (non-negotiable)" con el placeholder `{{design_tokens}}`, pero **ningún código lo rellena**
   (`costura.go:194` `scaffoldVarsFor` setea solo stack/language/project_name/lanes). El archivo
   scaffoldeado queda con el `{{design_tokens}}` **literal sin renderizar** → sección vacía.

7. **No hay imágenes reales en toda la cadena — excluidas estructuralmente.** La persona `designer`
   **prohíbe imágenes reales**: *"never an external image URL"* (`designer.md:151-152`). El mockup usa
   solo gradientes CSS + emoji. Ni art direction ni fuentes de imagen. Un build fiel hereda
   placeholders de gradiente/ícono (la app cae a `Icons.storefront`). Los productos premium se ven
   premium **en gran parte por la fotografía**; el pipeline no tiene estrategia de imágenes.

8. **La fundación de componentes compartidos está sub-dimensionada.** La story de design-system pide
   solo tres primitivos: *"Button, Card, Input"* (`scrum-master.md:74-80`). Chips, avatars, rows,
   badges, skeletons, toasts, date-strips se inventan ad hoc por pantalla → alimenta el mec. #4.

### Causa raíz, en una frase
> **Fluxo fragmenta un diseño premium en unidades de build aisladas, mínimas y gateadas por función,
> y le esconde al agente que construye cada unidad sus mejores artefactos visuales (el mockup, los
> tokens, las pantallas hermanas)** — así el agente optimiza "pasar los checkboxes funcionales de
> ESTA pantalla en aislamiento", que es justo el objetivo que produce UI competente-pero-genérica e
> inconsistente. Claude Code a mano gana porque una sesión humana sostiene toda la app + el mockup en
> contexto, itera visualmente, y nunca recibe la orden "nada más que el checkbox".

---

## Parte 2 — La verificación E2E: implementada, cableada, pero NO validó de verdad

**Veredicto: el código existe y está bien diseñado, pero en reservas-belleza nunca validó
integración.** Se mergeó con unit tests + que compila + review de LLM.

Evidencia `gh` de los runs en `aiudalabs/reservas-belleza`:

| Check | Qué pasó realmente |
|---|---|
| **e2e-verify** | Corrió **1 sola vez** (PR #57). Murió en el boot: *"backend did not become ready within 180s"* — el emulador de Firebase nunca levantó. `continue-on-error` lo pintó verde → **PR mergeado igual**. Flow/seed/invariantes nunca corrieron. |
| **ui-verify** (Playwright) | 3 runs, **todos los pasos SKIPPED** — el placeholder `{{app_path}}` quedó sin sustituir y apunta a `apps/customer` (las apps son `apps/client`/`apps/business`). Verde no-op. |
| **provisioning-lint** | Corrió, 0 FAIL / 2 WARN — `docs/provisioning.yaml` ausente → no pudo verificar frontera. |
| **suite-integrity** | Pasó — el único que gateó de verdad (unit/markers). |

Las 3 causas: (1) `e2e-verify.yml` corre en `ubuntu-latest` pelado, **sin `firebase-tools` ni JRE**
— asume una imagen pre-horneada (`fluxo-backend-fb`, el **S4 que nunca se construyó**). (2)
`ui-verify.yml` scaffoldeado **roto** (`{{app_path}}` sin renderizar). (3) `continue-on-error: true`
en todos — el **S5** (promover a bloqueante tras ir verde) nunca se alcanzó porque el único run real
se fue a rojo.

---

## La síntesis: son la MISMA falla

Las dos preguntas convergen: **nada en el pipeline premia ni verifica la calidad.**
- La construcción está gateada por ACs **funcionales**; la calidad visual no está en el contrato.
- El único gate que podría atrapar UI fea (ui-verify con su screenshot) **no juzga el screenshot**, y
  encima está roto. e2e-verify (integración) **ni siquiera arranca**.
- Entonces: "compila + unit tests pasan + arranca" es todo lo que se exige. Ni la funcionalidad de
  integración ni la estética tienen un juez. La UI mediocre pasa limpia **porque nada la frena**.

---

## Fixes de mayor palanca (ordenados)

1. **Consumir `screen_key`:** en export/dispatch, recortar la pantalla de `UI_SCREENS.md` + su mockup
   de `docs/mockups/*.html` e inyectarlos en el issue/prompt. Hacer el mockup un input de build de
   primera clase. (`export.go:issueBody`, `dispatch.go:buildPrompt`.)
2. **Gate de director de arte:** pasar el screenshot de `ui-verify` + el mockup a un modelo de visión
   que puntúe fidelidad visual; bloqueante-en-severo. Hoy el screenshot se captura y se tira.
3. **Restaurar el enriquecimiento por-story** (story-detailer o equivalente) en el path GitHub-native:
   expandir el skeleton ligero con screen spec + mockup + tokens **antes** del dispatch.
4. **Cambiar el contrato de la lane UI:** de "nada más que el checkbox" a "cumplí los ACs *y* igualá
   la calidad del mockup; reusá `packages/ui`, no re-inventes chrome". Generar ACs visuales.
5. **Arreglar `{{design_tokens}}`** — poblarlo desde `DESIGN_SYSTEM.md` al scaffoldear.
6. **Ampliar la story de fundación** a un set completo de componentes + regla dura "reusar, no
   re-implementar" (mata la duplicación del mec. #4).
7. **Estrategia de imágenes real** para la app construida (fotos curadas/seed, pipeline de avatares) —
   aflojar la regla "no external image URL" para el output Flutter.
8. **Arreglar la infra de e2e-verify** (instalar firebase-tools+JRE o construir la imagen `fluxo-backend-fb`
   del S4) + arreglar el render de `ui-verify` + promover a bloqueante (S5) una vez verde.

**Dónde empezar:** #1 + #4 (que el agente VEA el mockup y tenga el mandato de igualarlo) atacan la
raíz con el menor cambio. #2 (juez visual) es el que cierra el loop de calidad. El resto amplifica.
