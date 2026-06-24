// Configuración del cliente. La URL del control-plane viene de VIBEFORGE_API_URL
// (default http://localhost:8080). Exponemos NEXT_PUBLIC_* para que el browser pueda
// hablar directo con la API y el WS.

export const API_URL =
  process.env.NEXT_PUBLIC_VIBEFORGE_API_URL?.replace(/\/$/, "") ||
  "http://localhost:8080";

// Forzar modo mock aunque la API responda (demo / desarrollo de UI sin backend).
export const FORCE_MOCK = process.env.NEXT_PUBLIC_FORCE_MOCK === "1";

// Deriva el endpoint WS a partir de la URL HTTP del control-plane.
export function wsUrl(): string {
  const base = API_URL.replace(/^http/, "ws");
  return `${base}/ws`;
}

// Cuánto esperamos a que la API responda antes de caer a mock (ms).
export const HEALTH_TIMEOUT_MS = 2500;
