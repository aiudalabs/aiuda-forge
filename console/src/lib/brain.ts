// Brain API client — the per-project conversational assistant. Self-contained
// (does not touch the large api.ts): talks to the control plane's /assistant
// endpoints and streams responses via the shared WS bus (subscribe()).
import { API_URL } from "./config";
import { authHeaders, handleUnauthorized } from "./auth";

export interface BrainMessage {
  role: string; // "user" | "assistant"
  content: string;
  created_at?: number;
}

export interface ProposedAction {
  action_id: string;
  tool: string;
  args: Record<string, unknown>;
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...authHeaders(), ...(init?.headers || {}) },
  });
  if (res.status === 401) {
    handleUnauthorized();
    throw new Error("401 unauthorized");
  }
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`${init?.method || "GET"} ${path} → ${res.status} ${body}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Send a user message; the turn runs server-side and streams over the WS bus. */
export function sendAssistantMessage(projectId: string, message: string): Promise<{ conversation_id: string }> {
  return call(`/projects/${projectId}/assistant`, { method: "POST", body: JSON.stringify({ message }) });
}

/** Load the persisted conversation for a project. */
export function getAssistantHistory(projectId: string): Promise<{ conversation_id: string; messages: BrainMessage[] }> {
  return call(`/projects/${projectId}/assistant/history`);
}

export function approveAction(projectId: string, actionId: string): Promise<unknown> {
  return call(`/projects/${projectId}/assistant/actions/${actionId}/approve`, { method: "POST" });
}

export function rejectAction(projectId: string, actionId: string): Promise<unknown> {
  return call(`/projects/${projectId}/assistant/actions/${actionId}/reject`, { method: "POST" });
}
