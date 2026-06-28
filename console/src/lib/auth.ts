// Auth del console: token de sesión Bearer para el control-plane (audit C1).
//
// El control-plane hace auth OBLIGATORIA cuando hay un usuario o un service token
// configurado: toda llamada (salvo /healthz y POST /auth/login) necesita
// "Authorization: Bearer <token>". Aquí guardamos el token (memoria + localStorage),
// lo inyectamos en cada fetch (lib/api), y redirigimos a /login en un 401.
//
// MODO MOCK: cuando la API no responde, lib/api cae a datos de ejemplo y NUNCA
// llega aquí — el mock no requiere auth.

import { API_URL } from "./config";

const STORAGE_KEY = "vibeforge.token";

// Token en memoria (fuente de verdad durante la sesión) + espejo en localStorage
// para sobrevivir recargas. SSR-safe: window puede no existir.
let memoryToken: string | null = null;

/** Devuelve el token actual (memoria, con fallback a localStorage). */
export function getToken(): string | null {
  if (memoryToken) return memoryToken;
  if (typeof window === "undefined") return null;
  memoryToken = window.localStorage.getItem(STORAGE_KEY);
  return memoryToken;
}

/** Guarda el token en memoria y localStorage. */
export function setToken(token: string): void {
  memoryToken = token;
  if (typeof window !== "undefined") {
    window.localStorage.setItem(STORAGE_KEY, token);
  }
}

/** Borra el token (logout / 401). */
export function clearToken(): void {
  memoryToken = null;
  if (typeof window !== "undefined") {
    window.localStorage.removeItem(STORAGE_KEY);
  }
}

/** Cabeceras de auth para un fetch: { Authorization: Bearer <token> } o {}. */
export function authHeaders(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

/**
 * Maneja un 401: borra el token y redirige a /login (preservando el destino para
 * volver tras el login). No-op en SSR o si ya estamos en /login.
 */
export function handleUnauthorized(): void {
  clearToken();
  if (typeof window === "undefined") return;
  if (window.location.pathname === "/login") return;
  const next = encodeURIComponent(window.location.pathname + window.location.search);
  window.location.href = `/login?next=${next}`;
}

export interface LoginUser {
  id: string;
  email: string;
  created_at: number;
}

/** POST /auth/login — devuelve el token y lo persiste. Lanza Error en credenciales malas. */
export async function login(email: string, password: string): Promise<LoginUser> {
  const res = await fetch(`${API_URL}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) {
    if (res.status === 401) throw new Error("Email o contraseña inválidos.");
    const body = await res.text().catch(() => "");
    throw new Error(body || `Login falló (${res.status})`);
  }
  const data = (await res.json()) as { token: string; user: LoginUser };
  setToken(data.token);
  return data.user;
}

/**
 * POST /auth/register — alta self-service. Crea la cuenta y deja la sesión iniciada
 * (persiste el token). Lanza Error con mensaje claro en duplicado (409) o password
 * débil (400).
 */
export async function register(email: string, password: string): Promise<LoginUser> {
  const res = await fetch(`${API_URL}/auth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!res.ok) {
    if (res.status === 409) throw new Error("Ya existe una cuenta con ese email.");
    const body = await res.text().catch(() => "");
    throw new Error(body || `El registro falló (${res.status}).`);
  }
  const data = (await res.json()) as { token: string; user: LoginUser };
  setToken(data.token);
  return data.user;
}

/** GET /auth/me — usuario de la sesión actual, o null si no hay sesión. */
export async function me(): Promise<LoginUser | null> {
  try {
    const res = await fetch(`${API_URL}/auth/me`, { headers: { ...authHeaders() } });
    if (!res.ok) return null;
    const data = (await res.json()) as { user: LoginUser | null };
    return data.user;
  } catch {
    return null;
  }
}

/** POST /auth/logout — invalida la sesión en el servidor y borra el token local. */
export async function logout(): Promise<void> {
  const t = getToken();
  if (t) {
    await fetch(`${API_URL}/auth/logout`, {
      method: "POST",
      headers: { ...authHeaders() },
    }).catch(() => {
      /* logout es best-effort: borramos el token local pase lo que pase */
    });
  }
  clearToken();
}

/**
 * Decide si esta instancia del control-plane EXIGE auth. Llama a GET /auth/me sin
 * token: si responde 401, la auth está activa (necesitamos login); si responde
 * 200/503/etc, la auth está abierta (modo loopback dev) y no hace falta login.
 * Devuelve true cuando se necesita login.
 */
export async function authRequired(): Promise<boolean> {
  try {
    const res = await fetch(`${API_URL}/auth/me`, { headers: { ...authHeaders() } });
    return res.status === 401;
  } catch {
    // La API no responde → modo mock; no se requiere login.
    return false;
  }
}
