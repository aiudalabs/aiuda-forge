// Conexión WS al event bus del kernel (`/ws`). Un solo socket compartido; los componentes se
// suscriben con un listener. En modo mock no se conecta (la UI simula el stream desde lib/mock).
//
// Patrón: singleton con reconexión por backoff. Cada mensaje del bus es un RunEvent (doc 14 §B).

"use client";

import { mapEvent, type KernelEvent } from "./api";
import { wsUrl } from "./config";
import { getToken } from "./auth";
import type { RunEvent } from "./types";

type Listener = (ev: RunEvent) => void;

let socket: WebSocket | null = null;
let listeners = new Set<Listener>();
let backoff = 1000;
let connecting = false;
let enabled = false;

function connect() {
  if (typeof window === "undefined" || connecting || socket) return;
  connecting = true;
  try {
    // El navegador no puede poner cabecera Authorization en un WebSocket, así que
    // el token va como query param ?token= (el middleware lo valida igual, audit C1).
    const token = getToken();
    const url = token ? `${wsUrl()}?token=${encodeURIComponent(token)}` : wsUrl();
    const ws = new WebSocket(url);
    ws.onopen = () => {
      connecting = false;
      backoff = 1000;
    };
    ws.onmessage = (msg) => {
      try {
        // El bus emite el shape del kernel (seq/type/data/created_at); lo
        // traducimos al RunEvent de la UI con el mismo mapper que el REST.
        const ev = mapEvent(JSON.parse(msg.data) as KernelEvent);
        listeners.forEach((l) => l(ev));
      } catch {
        // ignorar frames no-JSON (heartbeats, etc.)
      }
    };
    ws.onclose = () => {
      socket = null;
      connecting = false;
      if (enabled) scheduleReconnect();
    };
    ws.onerror = () => {
      ws.close();
    };
    socket = ws;
  } catch {
    connecting = false;
    if (enabled) scheduleReconnect();
  }
}

function scheduleReconnect() {
  setTimeout(() => {
    if (enabled) connect();
  }, backoff);
  backoff = Math.min(backoff * 2, 15000);
}

/** Suscribe un listener al bus. Devuelve la función de baja. */
export function subscribe(fn: Listener): () => void {
  listeners.add(fn);
  enabled = true;
  connect();
  return () => {
    listeners.delete(fn);
    if (listeners.size === 0) {
      enabled = false;
      socket?.close();
      socket = null;
    }
  };
}
