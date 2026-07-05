# Skill — opinionated-defaults

Estructura del **`docs/CONSTITUTION.md`**: el doc pequeño, durable y bloqueado que TODAS
las fases posteriores leen. Es el ancla del modelo delta — el único doc que NO crece
por-feature. Manténlo corto (1–2 páginas). Decisiones, no prosa.

Deriva del brief. Donde el brief no fije algo, **elige tú** una opción por defecto y
justifícala en una línea — "depende" no es una decisión. Lo que quede genuinamente sin
resolver va a Open Questions, no se hedgea listando opciones sin elegir.

---

## Constitution

### 1. Producto — decisiones bloqueadas
Lista numerada de decisiones de producto que NO se re-litigan por-feature:
- CD-01: Público objetivo primario = …
- CD-02: Modelo de negocio = (marketplace | SaaS | freemium | …) + cómo cobra.
- CD-03: Plataformas/superficies = (móvil pasajero + web admin | …).
- CD-04: Idiomas soportados (i18n) = …

### 2. Stack (locked)
Tabla — Capa | Elección | Razón (1 línea) | Alternativa rechazada.
Cubre al menos: Backend, Frontend, DB, Auth, Infra/Deploy. Una elección por capa.

### 3. Presupuestos no-funcionales (los umbrales que gobiernan todo)
- NFR-Perf: p99 < … ms; presupuesto de tamaño de bundle < … .
- NFR-Seguridad: modelo de auth, tenencia (single/multi-tenant), datos sensibles.
- NFR-Disponibilidad / Escala: objetivo realista para v1 (no sobre-ingeniería).
- NFR-Accesibilidad / i18n: nivel comprometido.

### 4. Principios de ingeniería (guardrails)
3–6 reglas que los agentes de build deben respetar siempre (ej. "sin dependencias nuevas
sin justificar", "máx 2 niveles de anidación", "tests antes de refactor"). Cortas.

### 5. Explícitamente fuera de alcance (para siempre, no solo v1)
Lo que este producto NO va a hacer — reduce scope creep en cada iteración futura.

### 6. Open Questions
Solo lo que genuinamente bloquea y necesita input humano. Numerado.

---

**Barra de calidad**: una decisión por línea, cada una con su porqué en una frase. Si una
sección crece más allá de unas pocas viñetas, probablemente es contenido de PRD/arquitectura
mal ubicado — muévelo. La constitución se lee entera en 2 minutos.
