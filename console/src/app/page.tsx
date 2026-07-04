import { Studio } from "@/components/studio/Studio";

// Studio es la landing: el punto de entrada es siempre "¿qué quieres construir?".
// Cuando hay proyecto activo muestra las tabs Especificación / Diseño.
// La Overview sigue disponible desde el nav para quien quiera el resumen.
export default function HomePage() {
  return <Studio />;
}
