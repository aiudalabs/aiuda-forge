# Skill — data-model-template

Estructura del **`docs/DATA_MODEL.md`**: el modelo de datos del producto. Deriva del PRD:
cada entidad debe trazarse a ≥1 requisito. Es el contrato de datos que la arquitectura
implementa y las pantallas consumen — sé preciso con nombres de campos y transiciones.

---

## Data Model

### 1. Entidades (una subsección por entidad)
Por cada entidad:
- **Propósito** (1 línea) + a qué FR(s) del PRD responde.
- **Campos**: tabla — nombre | tipo | obligatorio | indexado | notas (constraints).
- **Relaciones**: con qué otras entidades y cardinalidad (1:1, 1:N, N:M).

### 2. Máquinas de estado
Por cada entidad con ciclo de vida (order, ticket, run…):
- Estados posibles (lista cerrada).
- Transiciones legales: `origen → destino` + quién la dispara (usuario/sistema/agente) +
  la condición. Marca las transiciones ilegales que deben rechazarse.
- **Quién es dueño de la transición** (dónde se valida — servidor, siempre).

### 3. Ubicación de los datos (placement)
Dónde vive cada entidad y por qué (según el stack de la constitución: tabla SQL, colección,
cache, cola). Justifica cualquier duplicación/desnormalización.

### 4. Patrones de acceso y costo de queries
Las queries que el producto DEBE soportar (de las pantallas/FR). Por cada una: qué índice la
sirve y su costo anticipado. Marca las que escalan mal y cómo se mitigan.

### 5. Permisos (filosofía)
Quién puede leer/escribir cada entidad. Modelo de tenencia (single/multi-tenant) heredado de
la constitución. Reglas que el servidor hace cumplir.

### 6. Open Questions
Numerado — lo que la arquitectura necesita resuelto.

---

**Barra de calidad**: toda entidad se traza a un FR. Toda máquina de estado tiene un dueño de
transición del lado servidor y una lista cerrada de transiciones legales. Ninguna query de
pantalla queda sin un índice que la sirva.
