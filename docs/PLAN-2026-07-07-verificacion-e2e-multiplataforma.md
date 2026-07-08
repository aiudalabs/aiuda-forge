# Plan de diseño — Fluxo: de "verificar artefactos" a "verificar comportamiento"

**Autor:** Arquitecto principal · **Fecha:** 2026-07-07 · **Estado:** propuesta para planificación de sprints
**Insumos:** 5 informes de exploración (verificación competitiva, auditoría del gate, ejecución multi-plataforma, verificación E2E+provisioning, puente mockup→código) + sesión E2E real (marketpty / stack `aiuda-flutter-firebase`) que expuso 8 bugs invisibles a 121 tests unitarios.

> **Nota de método:** todo lo que sigue respeta la constitución del kernel (`engine/CLAUDE.md`: cero metodología en Go). La verificación vive en **DATA** — workflows scaffoldeados por stack (`engine/registry/templates/github-native/<stack>/`) + tablas en el registry. El único Go nuevo es *transporte* (un string opaco más por-lane) y *plumbing* (marcar checks como required). Esta es literalmente la lección de D8 y de Wave V2 (sacar el hash rígido por-comando).

---

## 1. El cambio de filosofía — de artefactos a comportamiento

### El principio rector

> **Un cambio no está verificado hasta que el sistema integrado lo ejecuta.** El código que pasa unit tests verdes no es la evidencia; es la hipótesis. La evidencia es la app real corriendo contra un backend real, ejercitando el flujo como lo haría un usuario.

### Por qué (el hilo común de los 8 bugs)

Los 8 bugs de marketpty cayeron **todos en la frontera código↔sistema-real** — plataforma (manifest, permisos, APK, Maps key), backend (reglas Firestore, índices, init del Admin SDK), deploy/infra (IAM del SA, doc `users/{uid}` de bootstrap), y UX implícita (sesión no persiste). Ninguno de los 121 tests los pescó porque **todos mockean exactamente esa frontera**. Fluxo hoy tiene 5 capas de verificación (`ui-verify` smoke, `suite-integrity`, `claude-review`, Copilot review, anti-tamper) y **las 5 comparten el mismo punto ciego: verifican artefactos en aislamiento, nunca comportamiento integrado.**

El mercado ya resolvió esto: Replit corre un subagente con browser real (Playwright) que navega como usuario y auto-corrige; Lovable/Firebase Studio aprovisionan backend+DB por proyecto y dan preview vivo. Copilot coding agent —arquitectura casi idéntica a Fluxo— tiene el **mismo gap** y confirma que es el diferenciador correcto.

### Qué implica concretamente

Cinco corolarios operativos que ordenan todo el plan:

1. **Cada bug se ataca en la capa MÁS BARATA donde es visible.** Estático (linter) > emulador (E2E por-PR) > deploy real (release-gate). Solo lo que un runner de PR no puede ver físicamente (IAM del proyecto real, índice *BUILT*, doc sembrado por la app) baja a deploy-time.
2. **Nunca sembrar lo que la app crea en runtime.** Sembrar `users/{uid}` con el Admin SDK esconde el bug #7. El flujo (signup→login) se ejercita como comportamiento; solo se siembra el estado de mundo que preexiste al usuario (providers, disponibilidad).
3. **La honestidad del verificador es determinista.** El agente puede borrar el test que falla → doble candado: conteo de marcadores (`suite-integrity` ya existe; se clona a `e2e-integrity`) + el `reviewer` juzga que el E2E ejercita el AC de verdad.
4. **Los requisitos de frontera deben nacer como contrato máquina en diseño (Gap F, la causa raíz).** El `reviewer` está capado a los ACs *by design*. Sin convertir "permisos de manifest / índices / roles IAM / reglas allow-deny / invariantes de sesión" en artefactos declarados aguas arriba, agregar jobs de CI solo ayuda para lo que alguien se acordó de escribir. **Este es el sprint fundacional.**
5. **El humano sigue siendo dueño del merge (`merge_mode: manual`).** Toda esta capa **baja el costo** de la revisión humana (evalúa un preview vivo en segundos, no un diff), no la reemplaza. Eso reduce el costo de un falso negativo del gate y nos deja mover rápido en autonomía.

---

## 1-bis. El stack como CONTRATO de verificación de primera clase (multi-stack por diseño)

> **Riesgo raíz (feedback del usuario, 2026-07-07):** diseñar la capa de verificación contra UN solo stack (flutter+firebase) casi garantiza filtrar supuestos de Firebase a la parte "genérica" — es exactamente cómo Fluxo se ganó el olor a "metodología hardcodeada" antes (el sello de hash por-comando de Wave V, que se rompía distinto por stack). **La única forma de encontrar la abstracción correcta es diseñarla contra DOS stacks desde el día uno.**

### El concepto es universal; solo cambia el mecanismo

Los 4 mecanismos de verificación son el MISMO concepto en todo stack. Lo que cambia es la herramienta — y por eso vive en DATA por-stack, nunca en el kernel:

| Concepto (universal) | flutter+firebase | react+postgres | react+supabase |
|---|---|---|---|
| **E2E: bootear backend real + correr app + hacer el flujo** | Firebase Emulator Suite | `services: postgres` (Actions) + `docker compose` | `supabase start` (stack local en Docker) |
| **Índices vs queries reales** | composite indexes Firestore | `CREATE INDEX`/migraciones vs queries del ORM | migraciones vs queries |
| **Roles/permisos (used vs declared)** | `roles/datastore.user` | GRANTs de DB por rol | RLS policies + `service_role` |
| **Reglas vs SDK cliente** | Firestore rules + `@firebase/rules-unit-testing` | RLS o tests de authz de la API | RLS policies vs queries del cliente |
| **Scaffold de plataforma** | android/ manifest + keys | env/build config web | env web + tipos generados |

### El artefacto: `stack.verify.yaml` (contrato por stack)

En vez de N workflows Firebase-específicos, hay **UNA lógica genérica de scaffold** (en el template `_common/`, no en Go) parametrizada por un contrato que cada stack declara. Un stack nuevo = una carpeta de DATA, cero código:

```yaml
# engine/registry/templates/github-native/<stack>/stack.verify.yaml
stack: react-supabase
platform: web                          # web | android | ios | (lista)
image: fluxo-web                        # lane→imagen (§3); el kernel solo transporta el string

backend:                                # cómo levantar el backend REAL en CI
  boot: "supabase start"                # firebase emulators:exec | docker compose up | supabase start
  ready_probe: "curl -sf localhost:54321/health"
  teardown: "supabase stop"

provisioning_lint:                      # qué reglas deterministas aplica el linter (§2A)
  index_rules: rules/supabase-indexes.yaml     # cómo detectar "query necesita índice" en ESTE stack
  authz_check: rls                             # firestore-rules | rls | api-authz-tests
  permission_matrix: rules/web-permissions.yaml # dependencia→config requerida (web: env vars; móvil: manifest)
  role_map: rules/supabase-roles.yaml          # API/SDK→rol/policy requerido

e2e:                                    # cómo correr el flujo (§2B)
  runner: playwright                    # playwright | flutter-integration | maestro
  seed: seed/                           # fixtures = estado de mundo (NUNCA lo que la app crea en runtime)
  flows: flows/                         # los flujos derivados de los ACs (por-story)
  invariants: [session_persists, no_client_over_read]   # invariantes universales que aplican a todo stack
```

El scaffolder genérico (`_common/verify.gen`) lee este contrato y **emite** los `provisioning-lint.yml` / `e2e-verify.yml` concretos del stack. El kernel Go sigue haciendo solo `scaffold.Render(dir, stack, vars)` — no sabe qué es Supabase.

### Regla de oro anti-fuga

**Ningún string de un stack particular (`firestore`, `datastore.user`, `AndroidManifest.xml`) puede aparecer en `_common/`.** Si aparece, es una fuga: mudalo al `stack.verify.yaml` o a una tabla de reglas del stack. Los dos stacks de referencia (§6) son el test de esta regla: si `react-supabase` sale del mismo `_common/` que `flutter-firebase`, la abstracción es real.

---

## 2. Capa de verificación E2E + provisioning-linter (la pieza central)

> Todo lo de §2 se describe con el ejemplo `flutter-firebase` por concreción, pero **cada pieza es una instancia del contrato §1-bis** — la misma lógica `_common/` emite el equivalente para `react-supabase`/`react-postgres` desde su `stack.verify.yaml`.

Dos checks nuevos por-stack, scaffoldeados por `costura.go::OnBacklogPublished` → `scaffold.Render/Apply` al `main` del repo del tenant, marcados **required** por branch-protection. El Conductor (`native.go`) ya auto-mergea solo con checks en verde y `workflow_approval auto_if_safe` **nunca** aprueba PRs que tocan `.github/workflows/**` → un check required es inviolable por el agente. **Cero cambios en el Conductor** (es un required check más).

### 2A · `provisioning-lint.yml` — el piso determinista más barato (pesca 6 de 8 bugs, sin backend)

100% determinista, table-driven. La tabla de reglas vive versionada en el registry (`provisioning.rules.yaml` por stack), no en Go. **Es el mayor valor/costo del plan: estático, sin emulador, sin preview.** Cuatro checks:

| Check | Qué hace | Bug que pesca |
|---|---|---|
| **A — Índices** | Parseo estático (AST/regex) de queries Firestore en Dart (`.where().orderBy()`) y TS. Aplica las reglas reales de Firestore (≥1 igualdad + orderBy en otro campo → índice compuesto) y cruza contra `firestore.indexes.json`. **Falla con el JSON exacto listo para pegar.** | **#8** (query "Mis Citas" sin índice compuesto) |
| **B — IAM uso vs declarado** | Parsea qué APIs de Google tocan las functions (`admin.firestore()`→`roles/datastore.user`, `admin.messaging()`→`roles/cloudmessaging.*`) y cruza contra `docs/provisioning.yaml`. **Used-but-undeclared = FALLA**; declared-but-unused = warning. | **#6** (SA sin `roles/datastore.user`) |
| **C — Reglas vs SDK cliente** | `@firebase/rules-unit-testing` contra el emulador con contexto **autenticado de cliente** (admin bypassa reglas — ese es el punto ciego). Toda colección que el código cliente lee DEBE tener test allow. | **#3** (regla de `bookings` deniega lectura al cliente) — *usa emulador* |
| **D — Scaffold + matriz permiso↔dependencia** | Linter de archivos/manifests: `lib/` existe pero falta `android/app/src/main/AndroidManifest.xml` → FALLA (#1). Matriz `geolocator`→`ACCESS_FINE_LOCATION`, `google_maps_flutter`→`com.google.android.geo.API_KEY` no-vacío (#2, #4). Grep de `admin.initializeApp()` top-level idempotente único (#5). | **#1, #2, #4, #5** |

**Falsos positivos (crítico):** solo flaggear el índice compuesto que Firestore *realmente* exige (nunca single-field); query no analizable → **warning visible "declará manualmente"**, nunca pasar en silencio; escape hatch `// fluxo:no-index-needed`. La matriz dependencia→permiso versionada en el registry evita la pudrición por-stack.

**Archivos nuevos:**
- `engine/registry/templates/github-native/aiuda-flutter-firebase/.github/workflows/provisioning-lint.yml.tmpl`
- `engine/registry/templates/github-native/aiuda-flutter-firebase/provisioning.rules.yaml` (la tabla: dependencia→permiso, API→rol)
- Equivalente en `python-fastapi-react/` (allí: migraciones Alembic vs modelos, GRANTs de DB, CORS/env, OpenAPI vs rutas)

### 2B · `e2e-verify.yml` — comportamiento contra backend real (pesca #3 #5 #7 #8 como comportamiento + invariantes)

Hermano de `ui-verify.yml`. Estructura:

1. **Boot del backend REAL emulado.** Firebase Emulator Suite (`firestore`+`auth`+`functions`+`database`+`storage`) con los **artefactos reales del repo**: `firestore.rules`, `firestore.indexes.json`, `storage.rules`, y las Cloud Functions compiladas y desplegadas al emulador. El emulador **enforcea reglas Y usa índices igual que prod** → #3 (deny real), #8 (`FAILED_PRECONDITION` con la URL del índice), #5 (callable en frío sin mock de `db`). La toolchain ya la instala `copilot-setup-steps.yml` (`firebase setup:emulators`) — hoy instalada y **sin usar como gate**.
2. **Seeding en dos capas separadas.** Fixtures vía Admin SDK = estado de mundo (providers, disponibilidad). El **flujo se maneja como usuario real** (signup→login por el cliente) → #7 (falta `users/{uid}`, que la app crea al loguear) se ejercita como comportamiento.
3. **Correr la app REAL.** Flutter: `flutter test integration_test/` sobre Chrome headless (barato, flujo funcional) + **job aparte con `reactivecircus/android-emulator-runner`** que empaqueta el APK y arranca el emulador Android — única red de seguridad de comportamiento para #1/#2/#4 (fallas de plataforma que Flutter-web no toca; el linter D las pesca estático y barato).
4. **Dos invariantes ESTÁNDAR baked en el scaffold** (ningún AC los pide → van como invariante, no derivados):
   - **Persistencia de sesión:** login → matar proceso → relanzar → aseverar sigue autenticado (el bug "siempre pide login").
   - **UI vs mockup** — ver §5 (`ui-fidelity`).

**Reparto determinista/agéntico:** el plan de test (AC→pasos+selectores+asserts) lo autora el `story-detailer` como `e2e-plan` (agéntico, ya lee PRD+ARCH JIT). El código del test lo escribe el dev en el **mismo diff** que ya se revisa. La ejecución es determinista (verde/rojo). La honestidad: `reviewer` (¿ejercita el AC de verdad?) + **`e2e-integrity`** determinista (clon de `suite-integrity`: cuenta `testWidgets(`/`test(` en `integration_test/`, `.spec.` en `e2e/`, falla si bajan).

**Falsos positivos:** versiones de emulador pinneadas, IDs/timestamps fijos, animaciones off, `waitFor`/polling (nunca `sleep`), retry **una** vez. Path filter (`apps/**`/`functions/**`) + siempre en el PR de integración de sprint. Screenshots + log del emulador como artifacts para el triage humano.

**Archivos nuevos:**
- `.../aiuda-flutter-firebase/.github/workflows/e2e-verify.yml.tmpl`
- Extensión de `story-detailer.md` para emitir el `e2e-plan`

### 2C · `release-gate` — lo que ningún runner de PR puede ver (deploy-time)

El runner de PR no tiene creds del proyecto real. A deploy-time (en `costura` / workflow de deploy): `gcloud projects get-iam-policy` diff contra `provisioning.yaml` (**#6 real**), `firebase deploy --only firestore:indexes` + poll a READY (**#8 el índice *BUILT*, no solo declarado**), seed idempotente del bootstrap (**#7 el `users/{uid}` inicial**). Cierra los huecos "proyecto nuevo".

### 2D · El lado declarado (Gap F — el sprint fundacional que habilita A y B)

El linter necesita contra qué comparar. Formalizar lo que hoy es prosa (`IAM_REQUIREMENTS.md`) en artefacto máquina:
- **`docs/provisioning.yaml`** autorado por el `architect` en la fase `architecture` de `design.yaml`: roles IAM por SA, índices requeridos (o `derive: true`), expectativas dependencia→permiso. Lo revisa el `arch_gate` existente — **sin gate nuevo**.
- El `scrum-master` convierte el "platform & integration checklist" del architect en ACs por-story.

**Archivos:** `engine/registry/agents/architect.md`, `scrum-master.md`, `engine/registry/workflows/design.yaml` (fase `architecture`, nuevo output).

### Tabla de colocación en el gate

| Check | Ataca | Capa | Det/Agéntico |
|---|---|---|---|
| `provisioning-lint` A/B/D | #1 #2 #4 #5 #6 #8 | PR estático | **Determinista** |
| `provisioning-lint` C | #3 | PR + emulador | Determinista |
| `e2e-verify` (emulador FB) | #3 #5 #7 #8 | PR + emulador | Det run / agéntico authoring |
| `e2e-verify` job Android | #1 #2 #4 | PR + emulador Android | Determinista |
| Invariante sesión | "siempre pide login" | PR | Determinista |
| `release-gate` | #6-real #7 #8-built | deploy-time | Determinista |

---

## 3. Imágenes multi-plataforma + routing

### El corte físico que ordena todo

GitHub Actions tiene una frontera dura: **iOS obliga macOS, y macOS no tiene Docker** → la "imagen iOS" no puede ser Docker (es el runner macOS de GitHub o una VM Tart pre-horneada en un Mac self-hosted). Y el **Copilot coding agent siempre corre en ubuntu** → la verificación iOS/emulador es *inherentemente un check de PR fuera de la sesión de authoring*. Esto parte el problema en **dos planos**:

- **Plano A — Authoring** (el agente escribe + corre unit tests): siempre ubuntu, imagen liviana por lane. El agente NO necesita el emulador (bootear un AVD dentro de la sesión de 20min quema el presupuesto de tiempo).
- **Plano B — Verification** (checks de PR que dan la señal fuerte): cada plataforma es su propio workflow disparado por el PR, con su `runs-on` + imagen. Ubuntu para web/backend/android; macOS para iOS.

### (a) Set de imágenes pre-horneadas (GHCR, `ghcr.io/aiudalabs/fluxo-*`, nightly build)

| Imagen | Toolchain | Usada por |
|---|---|---|
| `fluxo-web:node20` | node20, pnpm, **Playwright+Chromium `--with-deps`** | `ui-verify`/`ui-fidelity`, lane react |
| `fluxo-flutter:stable` | Flutter SDK pinneado, Dart, melos, node20 | flutter-dev authoring, web-flutter |
| `fluxo-backend-fb:node20` | node20, **firebase-tools + emuladores pre-fetch**, JRE | firebase-dev, `e2e-verify`, `provisioning-lint` C |
| `fluxo-python:3.12` | python3.12, uv/poetry, node20 | python-dev, backend-verify |

**Ganancia:** hoy cada run gasta 3–5 min bajando `subosito/flutter-action` + SDK + `playwright install`. Horneado = `docker pull` cacheado. Multiplicá por cada PR de cada proyecto.

**Android** no es un `container:` limpio (`android-emulator-runner` necesita `/dev/kvm` en el host): la "imagen Android" es en realidad el **cache del AVD+snapshot** (`actions/cache` sobre `~/.android/avd`) en GitHub-hosted, o una imagen de runner completa en self-hosted. **iOS**: `macos-14` + `xcode-select` pinneado (hosted), o VM Tart pre-horneada (self-hosted).

### (b) Routing tarea→imagen (dos superficies, y es lo elegante GitHub-native)

- **Plano A (authoring): routing por LANE→imagen.** La story ya trae `Owner` (lane). Gemelo exacto de `ModelByLane`/`ExecutorByLane` → **`ImageByLane`**. El Conductor resuelve la imagen y la pasa como input a `claude.yml`, que la usa en `container:`. (Confirmado en repo: `ExecutorByLane` vive en `projects.go:64` + `conductor/dispatch.go:30` con el `Policy`/`Candidate`/`fireChannel` que voy a clonar 1:1.)
- **Plano B (verification): routing por PATH del PR→workflow→runner.** No se enruta la story; se enruta el check por lo que el PR tocó (patrón que ya usa `ui-verify.yml`: `paths: apps/** packages/**`). PR toca `apps/mobile/**` → `android-verify` (+ `ios-verify` si habilitado); `functions/**` → `firebase`/`e2e-verify`; `apps/web/**` → `ui-verify`. Más robusto que adivinar la lane, con `if: hashFiles(...)` como defensa en profundidad.

### (c) Cómo se declara en el registry

- **Go (transporte puro):** `Settings.ImageByLane map[string]string` en `projects.go` (+ columna `image_by_lane TEXT DEFAULT '{}'` + validación, gemelo de `executor_by_lane`), `Policy.ImageByLane` + `Candidate.Image` en `conductor/dispatch.go`, input `image` en `fireChannel`. Ninguna decisión de plataforma entra al kernel.
- **DATA (lo que importa):** matriz `verify` en `registry/templates/github-native/<stack>/verify.yaml` — runner+imagen+`on`+`paths`+`mode` por plataforma (web/firebase/android/ios/backend-postgres). El scaffold expande `verify.yaml` a los `*-verify.yml`. La consola (Registry→Templates) ya edita estos templates → matriz editable sin tocar Go.

### (d) Tradeoffs costo/tiempo — qué corre cada PR vs bajo demanda

| Check | Runner | Multiplicador | Cadencia |
|---|---|---|---|
| Web+Playwright, Backend+Postgres, Firebase emulador | ubuntu | 1x | **cada PR** (path-filtered) |
| Android emulador **smoke** (1 API, AVD cache) | ubuntu+KVM | 1x, ~4–12 min | **cada PR** que toca mobile |
| Android **matriz** (varias API) | ubuntu+KVM | 1x, 15–40 min | **nightly** |
| **iOS simulador** | macOS | **10x**, ~$1.2–1.9/run | **bajo demanda (label `needs-ios`) + nightly/pre-release** |

**iOS es el único sumidero de dinero** (free tier ~15 corridas/mes) → nunca por-PR por default; label + nightly. Muchos bugs de Flutter-iOS se cazan igual en web+Android. Self-hosted Mac (VM Tart) se paga solo si iOS supera ~300–500 min macOS/mes sostenido.

**Archivos:** `_common/.github/workflows/claude.yml.tmpl` (input `image` + `container:`), `<stack>/verify.yaml` (nuevo), `<stack>/.github/workflows/{android,ios,backend}-verify.yml.tmpl` (nuevos), `projects.go`, `conductor/dispatch.go`, `internal/scaffold/scaffold.go` (expandir `verify.yaml`), + workflow nightly en el repo aiuda-forge que publica `ghcr.io/aiudalabs/fluxo-*`.

### (e) Propiedad, ciclo de vida y QUIÉN PAGA

- **Quién las crea:** Fluxo (AIuda Labs), NO cada proyecto. Un workflow nightly en el repo `aiuda-forge` buildea un set chico de imágenes base **compartidas** (`fluxo-web`, `fluxo-flutter`, `fluxo-backend-fb`, `fluxo-python`, a futuro `fluxo-ios`). Son solo toolchains (Flutter, Node, firebase-tools, emuladores) — **sin secretos** → pueden ser públicas.
- **Dónde viven:** GitHub Container Registry, `ghcr.io/aiudalabs/fluxo-*`. Públicas = almacenamiento **ilimitado y gratis**.
- **Ciclo de vida:** re-build nightly (o al cambiar un Dockerfile) con tag `:latest` + `:<fecha>`. Los proyectos pinean un tag para reproducibilidad; se bumpean en una PR de mantenimiento. Cleanup policy de GHCR para no acumular tags viejos.
- **Costo — el modelo, en una tabla:**

| Concepto | Costo | Quién paga |
|---|---|---|
| Almacenar imágenes (GHCR público) | ~$0 (ilimitado) | AIuda Labs |
| Buildear nightly | centavos (unos builds/semana) | AIuda Labs |
| **CORRER el verify de un proyecto** (los minutos de Actions) | el costo real — ver tabla §3(d) | **el TENANT** (corre en SU GitHub org) |

  → Propiedad SaaS clave: **el cómputo pesado lo paga el cliente en sus propias Actions**; Fluxo solo paga build+storage de las imágenes base (~$0). La palanca de costo a vigilar es **macOS/iOS (10×) y el emulador Android** → se corren selectivamente (§3d), no por-PR.

- **Quién/cómo las consume:** el workflow de verify referencia `container: ghcr.io/aiudalabs/fluxo-web:<tag>`; el job corre **dentro** de la imagen pre-horneada → sin reinstalar toolchain (ahorra 3-5 min/run). El `image_by_lane` del registry elige la imagen por story; el kernel Go solo transporta el string.

---

## 4. Backends de preview por proyecto (estilo Lovable/Replit)

Dos niveles, deliberadamente separados — porque resuelven cosas distintas:

### 4A · Backend de **verificación** efímero por PR (ya cubierto por §2B)

El emulador Firebase booteado en `e2e-verify.yml` **es** el "backend vivo contra el que se testea" — efímero, por-run, con reglas+índices+functions reales. Esto es lo que hace creíble "el flujo funciona", y es lo que Fluxo necesita primero. No requiere infra nueva más allá de la imagen `fluxo-backend-fb`.

### 4B · Backend/preview **persistente** por proyecto (el modelo Lovable/Replit para el humano y la demo)

Lovable Cloud y Replit dan DB/auth/storage por proyecto sin config, y un **preview vivo** — es lo que hace que la app "salga andando a la primera" y lo que deja al humano del `merge_mode: manual` evaluar en segundos. Para el stack Firebase, el equivalente nativo:

1. **Ambiente `staging` por proyecto**, atado en Settings (proyecto GCP/Firebase del tenant, resuelto por el mismo `tenant.go` que ya resuelve credenciales GitHub por proyecto).
2. **Auto-deploy post-merge a staging**: un workflow scaffoldeado (`deploy-staging.yml`) que ante merge a `main` corre `firebase deploy` (functions, reglas, índices, hosting) — **y aloja el `release-gate` de §2C** (IAM diff, índice BUILT, seed bootstrap). Aquí, y solo aquí, se pescan #6/#7/#8-built.
3. **Preview vivo por PR (Lovable-style):** para lanes web, **Firebase Hosting preview channels** (`firebase hosting:channel:deploy pr-<n>`) dan una URL efímera por PR que el `ui-fidelity`/humano abren. Es la traducción directa del "preview embebido" de la competencia al mundo GitHub-native, reusando la infra egress que ya existe.

**Orden pragmático:** 4A primero (es el gate de comportamiento, prioridad); 4B después (mejora la demo y baja el costo de revisión humana, pero no es lo que pescó los 8 bugs).

**Archivos:** `<stack>/.github/workflows/deploy-staging.yml.tmpl` (nuevo), settings de proyecto para el `staging_project_id` (gemelo de los campos que ya maneja `projects.go`), resolución por `tenant.go`.

---

## 5. Puente mockup→código + verificación visual

**Diagnóstico:** `design.yaml` produce `docs/DESIGN_SYSTEM.md` (tokens — lo único que leen los devs) y `docs/mockups/*.html` (composición, aprobada por humano en `mockups_gate`, **que nadie aguas abajo consume**). El dev tiene el *qué color/fuente* pero reconstruye el *dónde va cada cosa* desde prosa → la composición deriva. Se tira la decisión de layout más cara y ya validada.

**Solución — el mockup aprobado como tres vistas de un mismo artefacto** (sin transpiler HTML→Flutter que Fluxo mantenga — la cinta de correr que Wave V rechazó):

### (a) El mockup se vuelve VINCULANTE (autoría)

1. **`data-*` inertes en el mockup** (`designer.md` + skill `design-system`): cada `<section id="screen-…" data-screen="passenger.home">` con `data-region`/`data-layout`/`data-role`/`data-pin`. HTML válido, no cambia el render, sigue self-contained. Convierte la extracción del contrato en un **paseo determinista del DOM**, no una re-narración creativa.
2. **`docs/SCREEN_MANIFEST.yaml`** (el linchpin que hoy no existe): clave estable por pantalla (`passenger.home`) ligando mockup#anchor ↔ `UI_SCREENS.md` ↔ `app_route` ↔ lane. Sin esta clave nada aguas abajo encuentra "el mockup de esta story". Revisado dentro del `mockups_gate` existente — **sin gate nuevo**.
3. **`docs/LAYOUT_CONTRACTS/<key>.yaml`** — Layout IR extraído de los `data-*`: composición en primitivas neutrales (`row`/`column`/`list` + `fill`/`hug`/`fixed` + roles semánticos), **omite** px/color/fuente (esos son tokens). Mapea 1:1 a Flutter (`Column/Row/Expanded`), SwiftUI, React — sin transpiler.
4. **Inyección en el ticket** (`story-detailer.md`): para story frontend, resolver `screen_key` (nuevo campo del backlog que pone el `scrum-master`) y embeber las tres cosas: el contrato IR, el snippet `<section>` HTML, y el puntero al screenshot del mockup. El ticket pasa de "tokens+prosa" a "tokens + composición vinculante + referencia visual".

### (b) `ui-fidelity.yml` — gate de fidelidad visual (juez agéntico, no pixel-diff)

Hermano de `ui-verify`, construido sobre su harness. Por cada `screen_key` tocado por el PR:
1. **Screenshot de la app real**: build web → navegar a `/__catalog/<key>` a viewport fijo (el del mockup) → capturar.
2. **Screenshot del mockup**: Chromium carga el `<section>`, mismo viewport, captura (determinista).
3. **Juez vision agéntico**: recibe ambos + el `LAYOUT_CONTRACTS/<key>.yaml` + el volcado del árbol de widgets/DOM real. **Puntúa composición, no píxeles** (pixel-diff daría ~100% distinto: Flutter pinta en canvas). Rúbrica con hard-fails críticos: presencia/anidamiento de regiones, orden de hijos, elementos anclados (CTA pinned bottom). **Ignora** colores/fuentes/copy/spacing sub-pixel. Salida: score + lista enumerada de desviaciones → el `feedback` que reencola al dev (mismo patrón `on_fail` de los gates). Política: `overall ≥ 80` Y cero críticos.

**Infra nueva real (única):** el **screen-catalog** — la story fundacional del design-system (que el `scrum-master` ya construye PRIMERO) wirea una ruta debug web `/__catalog/<screen_key>` que monta la pantalla con fixtures. Va en `ARCHITECTURE.md` + story fundacional.

**Nota adversarial obligatoria:** dev sirve un `<img>` del mockup como "pantalla" → el juez recibe el árbol de widgets real y exige roles interactivos; `ui-verify` ya exige no-blank. Juez indulgente → hard-fails críticos + el humano sigue dueño del merge (el juez BAJA el costo, no lo reemplaza).

**Retro-compatibilidad:** todo aditivo. Sin `SCREEN_MANIFEST`/`LAYOUT_CONTRACTS`, `story-detailer` degrada a hoy y `ui-fidelity` se salta con `hashFiles` (verde). Opt-in por proyecto.

**Archivos:** `designer.md`, `story-detailer.md`, `scrum-master.md`, `<stack>/.github/instructions/app.instructions.md.tmpl` (sección "Fidelidad de layout"), `<stack>/.github/workflows/ui-fidelity.yml.tmpl` (nuevo), `design.yaml` (fase `mockups`: outputs nuevos), skill `design-system`.

---

## 6. Roadmap por sprints

**Regla de priorización (pedido explícito del usuario):** el loop de verificación va primero. No hay prisa — queremos SÓLIDO. Por eso el orden ataca **cobertura de bugs por costo creciente**: primero lo determinista-estático-barato (pesca 6 de 8 sin infra), luego el comportamiento con emulador, luego multi-plataforma, luego el puente visual, luego el preview vivo.

**Regla MULTI-STACK (feedback del usuario, §1-bis):** cada pieza de verificación se construye **contra DOS stacks de referencia en paralelo desde el arranque** — `flutter-firebase` (móvil+Firebase, ya lo conocemos) y `react-supabase` (web+RLS, el más distinto). No se declara "listo" un check hasta que **ambos stacks salen del mismo `_common/`**. Esto convierte la abstracción de aspiración a hecho verificado, y evita hornear supuestos de Firebase en la capa genérica. El costo extra es real pero acotado (el segundo stack es DATA, no lógica nueva) y es exactamente lo que compra "mañana react+postgres es una carpeta, no un port".

**Leyenda:** 🟢 quick-win · 🔵 fundacional · S/M/L esfuerzo relativo · 🧩 toca los 2 stacks de referencia.

---

### Sprint 1 — El contrato declarado (Gap F) + branch-protection plumbing 🔵 · **M**
**Prioridad: MÁXIMA (habilita todo el resto).** Sin el "lado declarado", los linters solo pescan lo que alguien escribió.
- `docs/provisioning.yaml` como output del `architect` en `design.yaml` fase `architecture` (roles IAM, índices, dependencia→permiso). — *registry: `architect.md`, `design.yaml`*
- `scrum-master` emite `screen_key` por story frontend + convierte el platform-checklist del architect en ACs. — *registry: `scrum-master.md`*
- Plumbing de branch-protection en `internal/scaffold` para marcar checks nuevos como **required** (para que el auto-merge del Conductor los espere sin tocar `native.go`). — *Go: `internal/scaffold/scaffold.go`*
- **Toca:** solo registry (.md + design.yaml) + un helper de scaffold. **Sin kernel de decisión.**
- **Dependencias:** ninguna. **Bloquea:** Sprints 2 y 5.

### Sprint 2 — El contrato `stack.verify.yaml` + `provisioning-lint` en 2 stacks 🟢🔵🧩 · **M/L**
**EL quick-win de mayor impacto: pesca #1 #2 #4 #5 #6 #8 — 6 de 8 bugs — estático, sin emulador, sin preview.**
- **Primero el contrato:** definir `stack.verify.yaml` (§1-bis) + el scaffolder genérico en `_common/verify.gen` que lo consume. Este es el paso que hace todo lo demás multi-stack.
- Checks A (índices), B (IAM/roles uso vs decl), D (scaffold+matriz permiso) **como lógica genérica en `_common/`, parametrizada por las tablas de reglas del stack**. Check C (reglas/RLS vs cliente) necesita emulador → puede diferirse a Sprint 3.
- **Se instancia para LOS DOS stacks a la vez:** `flutter-firebase` (índices Firestore, `datastore.user`, manifest) **y** `react-supabase` (migraciones vs queries, GRANTs/RLS, env web). Si ambos salen del mismo `_common/`, la abstracción quedó probada; si `react-supabase` necesita un caso especial en `_common/`, ahí se detecta la fuga (regla de oro §1-bis).
- **Toca:** `_common/verify.gen` + `_common/provisioning-lint.yml.tmpl` (genérico) + `flutter-firebase/stack.verify.yaml` + `react-supabase/stack.verify.yaml` + las tablas de reglas de cada stack (DATA). Cero Go. Marcado required (Sprint 1).
- **Dependencias:** Sprint 1. **Retorno más alto por unidad de esfuerzo — el primer entregable de valor.**

### Sprint 3 — `e2e-verify` (backend real) en 2 stacks 🔵🧩 · **L**
**Comportamiento real: pesca #3 #5 #7 #8 como comportamiento + invariante de persistencia de sesión.**
- `_common/e2e-verify.yml.tmpl` genérico que lee `stack.verify.yaml.backend` para bootear el backend real: `firebase emulators:exec` (flutter-firebase) vs `supabase start` (react-supabase). Seeding en dos capas (fixtures = mundo; el flujo signup→login se ejercita, no se siembra), runner por-stack (`flutter-integration` vs `playwright`), invariantes universales (`session_persists`, `no_client_over_read`), `e2e-integrity` (clon de suite-integrity).
- `story-detailer` emite el `e2e-plan` por story (agnóstico); el dev escribe el test en el mismo diff con el runner del stack.
- **Los 2 stacks validan la abstracción del backend:** si el mismo `_common/e2e-verify` corre Firebase-emulador Y Supabase-local sin ramas hardcodeadas, el contrato `backend:` es correcto.
- **Toca:** `_common/e2e-verify.yml.tmpl`, `story-detailer.md`, los `stack.verify.yaml` de ambos. Usa las imágenes de Sprint 4 (o setup-action interino).
- **Dependencias:** Sprint 1 (ACs de frontera) + Sprint 2 (el contrato). Se beneficia de Sprint 4 pero no lo bloquea.

### Sprint 4 — Imágenes horneadas + `image_by_lane` + generador `verify.yaml` 🟢🔵 · **M**
**Habilitador transversal: velocidad (saca 3–5 min/PR) y la superficie multi-plataforma.**
- Hornear las 4 imágenes Docker Linux a GHCR (workflow nightly en aiuda-forge). — 🟢 *ganancia inmediata, independiente*
- `image_by_lane` (Go transporte): clonar `executor_by_lane` en `projects.go` + `Policy`/`Candidate`/`fireChannel` en `conductor/dispatch.go` + input `image` en `claude.yml.tmpl`. — 🟢 *bajo riesgo, gemelo probado*
- `verify.yaml` → generador en `internal/scaffold`; empezar con web+firebase+backend(services).
- **Dependencias:** ninguna dura (el nightly de imágenes se puede hacer en paralelo desde el Sprint 1). **Recomendado en paralelo con 2/3.**

### Sprint 5 — `android-verify` (emulador Android) 🔵 · **L**
**Red de seguridad de comportamiento para #1 #2 #4** (el linter D ya los pesca estático; esto los confirma en dispositivo).
- `reactivecircus/android-emulator-runner` en ubuntu+KVM, AVD cache, smoke 1-API por-PR, matriz nightly. Empaqueta APK real → pesca fallas de plataforma que Flutter-web no toca.
- **Toca:** `<stack>/verify.yaml` (bloque android) → `android-verify.yml.tmpl`. **Dependencias:** Sprint 4 (generador + routing por path).

### Sprint 6 — Puente mockup→código (autoría) 🔵 · **M**
- `data-*` en mockups + `SCREEN_MANIFEST.yaml` + `LAYOUT_CONTRACTS/` (designer, revisado en `mockups_gate`); inyección en el ticket (`story-detailer`); sección "Fidelidad de layout" en `app.instructions`.
- **Toca:** `designer.md`, `story-detailer.md`, `scrum-master.md`, `app.instructions.md.tmpl`, `design.yaml`, skill `design-system`. Todo aditivo/opt-in.
- **Dependencias:** Sprint 1 (`screen_key` en el backlog).

### Sprint 7 — `ui-fidelity.yml` (gate visual) + screen-catalog 🔵 · **L**
- Screenshot app real vs mockup, juez vision agéntico (composición, no píxeles), screen-catalog `/__catalog/<key>` (única infra nueva) wireado por la story fundacional del design-system.
- **Toca:** `ui-fidelity.yml.tmpl`, story fundacional (`scrum-master`), `ARCHITECTURE.md` (dev-workflow). **Dependencias:** Sprint 6 (contratos+manifest) + Sprint 4 (imagen `fluxo-web`).

### Sprint 8 — `release-gate` + preview vivo por proyecto 🔵 · **L**
- `deploy-staging.yml` post-merge: `release-gate` (gcloud IAM diff → **#6 real**, `firebase deploy --only firestore:indexes` + poll READY → **#8 built**, seed bootstrap → **#7 real**). Firebase Hosting preview channels por PR (Lovable-style).
- **Toca:** `deploy-staging.yml.tmpl`, settings `staging_project_id` (projects.go), `tenant.go`. **Dependencias:** Sprint 4 (imágenes/routing).

### Sprint 9 (bajo demanda) — `ios-verify` + self-hosted 🔵 · **L**
- `macos-14` disparado por label `needs-ios` + nightly/pre-release. Self-hosted Mac (Tart) solo si el consumo macOS lo justifica (>300–500 min/mes).
- **Dependencias:** Sprint 5. **No bloqueante** — iOS como gate de release, no de cada commit.

---

### Grafo de dependencias (resumen)

```
S1 (contrato declarado) ──┬──> S2 (provisioning-lint)  ── pesca 6/8, ESTÁTICO ← ROI máximo
                          ├──> S3 (e2e-verify emulador) ── pesca #3#5#7#8 comportamiento
                          └──> S6 (mockup autoría) ─────> S7 (ui-fidelity visual)
S4 (imágenes + image_by_lane + verify.yaml) ──┬──> S5 (android-verify) ──> S9 (ios, on-demand)
   [paralelizable desde S1]                    └──> S8 (release-gate + preview vivo)
```

### Cobertura de los 8 bugs por sprint

| Bug | Dónde se cierra |
|---|---|
| #1 scaffold android/ | S2 (lint D, estático) + S5 (Android, comportamiento) |
| #2 ACCESS_FINE_LOCATION | S2 (lint D) + S5 |
| #3 regla bookings deny | S2 (lint C, emulador) + S3 (e2e comportamiento) |
| #4 Maps key manifest | S2 (lint D) + S5 |
| #5 Admin SDK no-init | S2 (lint D grep) + S3 (callable en frío) |
| #6 SA sin datastore.user | S2 (lint B declarado) + S8 (release-gate, real) |
| #7 falta users/{uid} | S3 (flujo real, no sembrado) + S8 (seed bootstrap) |
| #8 índice compuesto | S2 (lint A, JSON listo) + S8 (índice *BUILT*). **NO es S3**: hallazgo empírico de S3 pt.2 — el Firebase Emulator enforcea reglas (por eso #3 cae) pero **corre queries compuestas sin índice igual**, no emite `FAILED_PRECONDITION`. Corrige el supuesto original de §2B. |
| sesión no persiste | S3 (invariante estándar) |
| UI lejos del mockup | S6+S7 (contrato + gate visual) |

**Lectura ejecutiva:** con **Sprints 1–3** (todo registry/DATA + un poco de scaffold, cero decisiones en el kernel) Fluxo pasa de "verifica artefactos" a "verifica comportamiento" y cierra **los 8 bugs a nivel PR**. Sprints 4–5 dan velocidad y la red de seguridad multi-plataforma; 6–7 cierran la deriva visual; 8 cierra los huecos de "proyecto nuevo" que solo el deploy real ve. El pedido del usuario —el loop de verificación como prioridad— se cumple poniendo `provisioning-lint` (S2) como el primer entregable de valor, por ser el más barato y el de mayor cobertura.

---

**Archivos clave a tocar (índice consolidado):**
- **DATA/registry (la mayoría):** `engine/registry/templates/github-native/aiuda-flutter-firebase/{provisioning.rules.yaml, verify.yaml, .github/workflows/{provisioning-lint,e2e-verify,android-verify,ui-fidelity,deploy-staging}.yml.tmpl, .github/instructions/app.instructions.md.tmpl}`; equivalentes en `python-fastapi-react/`; `_common/.github/workflows/claude.yml.tmpl`; `engine/registry/agents/{architect,scrum-master,story-detailer,designer}.md`; `engine/registry/workflows/design.yaml`; skill `design-system`.
- **Go (transporte + plumbing, mínimo):** `engine/internal/projects/projects.go` (`ImageByLane` + columna, gemelo de `executor_by_lane` en :64), `engine/internal/conductor/dispatch.go` (`Policy.ImageByLane`/`Candidate.Image`/`fireChannel`), `engine/internal/scaffold/scaffold.go` (expandir `verify.yaml`, branch-protection required), `engine/internal/app/app.go` y `engine/internal/api/dispatch.go` (cablear el nuevo campo, mismos sitios donde hoy vive `ExecutorByLane`).
- **CI del propio Fluxo:** workflow nightly que publica `ghcr.io/aiudalabs/fluxo-{web,flutter,backend-fb,python}`.

---

## 7. Plan de EJECUCIÓN — cómo construir esto sin romper nada, sesión por sesión

Principios de rollout (no negociables):

- **Aditivo y detrás de flag hasta que se pruebe.** Cada check nuevo nace como `continue-on-error: true` (visible pero NO required). Solo se marca **required** cuando ya pasó en verde en un proyecto real. Un check nuevo jamás puede tumbar el auto-merge del Conductor antes de estar validado.
- **Un stack existente NO se toca hasta migrarlo explícitamente.** Los `flutter-firebase` y `python-fastapi-react` actuales siguen igual; el trabajo agrega archivos nuevos (`stack.verify.yaml`, `_common/verify.gen`) que nadie consume hasta que se re-scaffoldea un proyecto. Proyectos vivos (marketpty, reservas-belleza) se re-scaffoldean **a mano y de a uno** (`POST /projects/{id}/scaffold/github`), no en masa.
- **Kernel Go: solo cambios de transporte, con test antes.** Cada toque en Go (el `image_by_lane`) va con su test unitario en el mismo PR y es un gemelo exacto de un patrón que ya existe (`executor_by_lane`). Cero lógica de decisión nueva.
- **Regla de las dos ramas de dogfooding:** validar cada sprint contra un proyecto real de CADA stack de referencia antes de cerrarlo — el flutter-firebase que ya tenemos + un react-supabase nuevo mínimo que se crea en el Sprint 2.

### Secuencia de sesiones (cada una es autocontenible y termina con algo verde)

| Sesión | Entrega | Criterio de "hecho" (sin romper nada) |
|---|---|---|
| **S0 — Andamiaje del 2º stack** | Crear el stack `react-supabase` MÍNIMO en el registry (carpeta + scaffold básico, SIN verificación aún) y un proyecto demo que compile. | Un proyecto react-supabase se scaffoldea y buildea. Nada de los stacks viejos cambia. |
| **S1 — Contrato declarado (Gap F)** | `architect` emite `docs/provisioning.yaml`; `scrum-master` emite `screen_key` + ACs de frontera. Helper de branch-protection (sin marcar nada required todavía). | Un design-run nuevo produce `provisioning.yaml`. Los proyectos viejos no lo tienen y siguen andando (el consumidor lo trata como opcional). |
| **S2a — Contrato `stack.verify.yaml` + `_common/verify.gen`** | El schema + el scaffolder genérico. Instanciado (vacío) para los 2 stacks. | `verify.gen` corre y emite workflows no-op para ambos stacks. Regla de oro §1-bis: cero strings Firebase en `_common/`. |
| **S2b — `provisioning-lint` (checks A/B/D)** | Los 3 linters deterministas, genéricos, con las tablas de reglas de cada stack. `continue-on-error: true`. | En un PR de prueba de CADA stack: el linter corre, reporta, NO bloquea. Reproduce los 8 bugs originales como fallo (test de regresión contra un repo con los bugs). |
| **S3 — `e2e-verify` (backend real)** | El workflow genérico + boot Firebase-emulator y Supabase-local desde el contrato. `continue-on-error: true`. | En cada stack: levanta el backend, corre 1 flujo E2E derivado de un AC, pasa. La sesión-no-persiste se detecta como fallo en un repo con el bug. |
| **S4 — Imágenes + `image_by_lane`** | Nightly que hornea `fluxo-{web,flutter,backend-fb}`; el Go de transporte (`image_by_lane`, gemelo de `executor_by_lane`) con su test. | Un dispatch route a la imagen correcta; los tests del kernel pasan; sin imagen declarada usa el default (retrocompatible). |
| **S5 — Promoción a required + dogfood** | Marcar `provisioning-lint` y `e2e-verify` como **required** SOLO en los 2 proyectos de dogfood, tras verlos verdes. Recién acá el gate "muerde". | Un PR con un bug de frontera es BLOQUEADO por el gate en ambos stacks. Los proyectos que no migraron siguen con el gate viejo. |
| **S6–S7 — Mockup vinculante + `ui-fidelity`** | El puente de autoría + el gate visual (juez visión). | Un mockup aprobado se le pasa al dev; el gate de fidelidad puntúa app-vs-mockup. `continue-on-error` → required tras validar. |
| **S8 — `release-gate` + preview vivo** | Los checks de deploy-time (IAM real, índice built, seed bootstrap) + el preview persistente por proyecto. | Un release valida provisioning contra el proyecto real; el humano evalúa un preview vivo. |

### Por qué este orden no rompe nada

1. **Todo lo nuevo es opt-in por diseño** (`continue-on-error` → required solo tras verde real). El Conductor nunca ve un check rojo que no validamos.
2. **El 2º stack (S0) va primero** para que S2/S3 ya tengan contra qué probar la abstracción — no se descubre la fuga después de hornear.
3. **El kernel Go se toca una sola vez (S4)** y es un gemelo probado de algo existente.
4. **La migración de proyectos vivos es manual y de a uno** — nunca un cambio de registry re-scaffoldea repos en masa.
5. **Cada sesión termina con algo verde y demostrable** (un PR de prueba que muestra el check corriendo), así el progreso es verificable y reversible.

### Prompt semilla para cada sesión fresca

Cada sesión nueva arranca con: *"Leé `docs/PLAN-2026-07-07-verificacion-e2e-multiplataforma.md` §7 y ejecutá la sesión **Sn**. Regla: aditivo, `continue-on-error` hasta validar, no tocar stacks/proyectos vivos, kernel solo transporte-con-test. Terminá con un PR de prueba verde en los 2 stacks de referencia."* — más el contexto de los 8 bugs (§1) como test de regresión.

---

## 8. Conexión de proveedores cloud — cerrar el ciclo definición→deploy

> **La visión (referencia Lovable/Replit):** desde la UI de Fluxo el usuario **vincula su cuenta** (Supabase / Firebase / GCP) y a partir de ahí Fluxo tiene credenciales para hacer TODO lo que hoy se hace a mano: deploy de functions, crear índices, otorgar los roles IAM que faltan, habilitar APIs, provisionar Storage/Auth. Sin esto, la fábrica genera código pero el "sale andando en tu cloud" queda como trabajo manual (exactamente los 8 remedios que se aplicaron a mano en la sesión E2E de 2026-07-07).

### Por qué es tracción directa: Fluxo YA tiene el patrón

Es idéntico a cómo Fluxo conecta GitHub hoy: GitHub App installation → token por-tenant (`auth.github_tokens`, `tenant.go`). Un proveedor cloud es **el mismo diseño con otra fuente de credencial**. No es arquitectura nueva; es un conector más.

### Mecanismo por proveedor

| Proveedor | Cómo se conecta | Qué desbloquea | Dificultad |
|---|---|---|---|
| **Supabase** | OAuth2 + Management API (tienen OAuth apps) → token del tenant | crear proyecto, migraciones, RLS, deploy — **ciclo completo automatizable, incluso crear el proyecto** | 🟢 |
| **Firebase/GCP** | Google OAuth (scope `cloud-platform`) · o service-account JSON · o Workload Identity Federation (sin llaves largas) | `firebase deploy`, crear índices, otorgar IAM, habilitar APIs, provisionar Storage/Auth — todo lo del incidente E2E | 🟡 (deploy) / 🔴 (crear proyecto+billing) |

### Dónde viven las credenciales (camino GitHub-native)

Dos opciones; el modelo GitHub-native prefiere la segunda:
1. **En Fluxo**, cifradas por tenant (como los tokens de GitHub).
2. **Como secrets del repo del tenant**: Fluxo, usando la conexión, inyecta `FIREBASE_SERVICE_ACCOUNT` / `SUPABASE_ACCESS_TOKEN` como GitHub Actions secrets del repo → el workflow de deploy los consume. El cómputo del deploy vuelve a correr en las Actions del cliente (mismo modelo de costo que §3e).

### Cómo cierra el ciclo (la conexión con el resto del plan)

El `provisioning.yaml` (Gap F, §2D) es la fuente de verdad de "qué necesita este proyecto en su cloud" (roles IAM, índices, APIs, buckets). Con la cuenta conectada, el workflow **`release-gate`/`deploy-staging`** (§2C, §4B) lo **lee y lo APLICA** contra el cloud del tenant. Los remedios manuales del incidente E2E se vuelven **pasos automáticos**: `diseño lo declara → deploy lo aplica → verify lo confirma`. Esto convierte a §2C/§4B de "correr `firebase deploy`" a "provisioning-as-code end-to-end".

### Fases (es ambicioso — de a poco)

| Fase | Qué | Dificultad | Cubre |
|---|---|---|---|
| **A** | Conectar cuenta → credenciales como secrets → el workflow corre `firebase deploy`/`supabase db push` | 🟢 media | ~80%: functions, rules, índices del `firestore.indexes.json`, migraciones |
| **B** | Los hallazgos del provisioning-linter (rol/índice/API faltante) se **auto-aplican** vía la conexión | 🟡 | el "otorgar permisos que faltan" |
| **C** | Fluxo **crea el proyecto** (Supabase Management API / creación GCP) — el modelo "backend por proyecto" | 🔴 alta | ciclo completo desde cero |

**Caveat honesto:** en GCP/Firebase, crear el proyecto + **billing (Blaze)** es la parte dura — en la sesión E2E se pegó contra la **cuota de billing** a mano, y requiere acceso al billing account + policies de org. **Supabase es mucho más fácil** (Management API crea proyectos en el plan del usuario sin fricción) → por eso Supabase es el 2º stack de referencia ideal: ejercita RLS (distinto a Firestore) **y** permite probar el ciclo definición→deploy completo sin el infierno del billing de GCP.

**Dónde entra en el roadmap:** la **Fase A con Supabase** es el punto de entrada de menor fricción y mayor demostración. Encaja después del Sprint 3 (ya hay `provisioning.yaml` + e2e), como habilitador de §4B (preview vivo). No bloquea la capa de verificación (§2/§3), que funciona con backend emulado sin cuenta conectada.

**Toca (a futuro, no en los sprints 1-5):** un nuevo módulo `internal/cloudconn/` (gemelo de `ghapp/`), UI de "Conectar Supabase/Firebase" en Settings (gemelo del flujo GitHub App), y el `deploy-staging.yml.tmpl` que consume los secrets. Todo el mecanismo de credenciales por-tenant ya existe como referencia en `tenant.go`.