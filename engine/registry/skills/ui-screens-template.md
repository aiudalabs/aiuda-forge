# Skill — ui-screens-template

Estructura del **`docs/UI_SCREENS.md`**: cada pantalla descrita en texto, suficiente para
que el designer haga los mockups y un dev construya sin adivinar. Deriva del PRD y la
arquitectura. NO es copy final — es estructura, estados y datos.

---

## UI Screens

### 1. Grafo de navegación (por superficie)
Por cada app/superficie (pasajero, conductor, admin…): lista de pantallas con IDs
jerárquicos (X.Y.Z) y qué lleva a qué. Marca la pantalla de entrada.

### 2. Design tokens (heredados de la constitución/brief)
- Semilla de color + paleta, sistema tipográfico, tamaño de tap-target por contexto.
- Si la constitución fijó una dirección de diseño, cíñete a ella; si no, propón y compromete una.

### 3. Inventario de componentes reutilizables
Lista de componentes que aparecen en ≥2 pantallas (card, list-item, sheet, empty-state…).
Cada uno: propósito + variantes. Evita rediseñar lo mismo por pantalla.

### 4. Pantallas (una subsección por pantalla)
Por cada pantalla, con su ID (X.Y.Z):
- **Header**: título + acciones.
- **Body**: qué muestra, en qué orden, con qué datos (referencia la entidad del DATA_MODEL).
- **CTA primaria**: la acción principal + a dónde navega.
- **Navegación**: entradas y salidas.
- **Estados**: vacío / cargando / error (los tres, siempre — no solo el happy path).
- **Datos**: qué requisito(s) FR y qué entidad(es) alimentan la pantalla.

### 5. Pantallas clave para mockup (elige 5 por superficie)
Las 5 que el designer prototipa en HTML — las del core loop, no las de settings.

### 6. Open Questions
Numerado — lo que el designer/dev necesita resuelto.

---

**Barra de calidad**: cada pantalla declara sus tres estados (vacío/cargando/error) y liga
sus datos a una entidad del modelo de datos y a un FR del PRD. Un dev debe poder construir la
pantalla sin preguntar qué mostrar cuando no hay datos.
