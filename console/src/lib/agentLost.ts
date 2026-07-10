// agentLost — la ÚNICA decisión de "¿esta story tiene un agente perdido?" que
// consumen la card del Kanban (badge) y el TicketDetail (banner + botón de
// recuperación). El conductor pone la nota `agent_lost` cuando declara muerta la
// sesión de un agente (task de Copilot purgada / label agent:running stale) y
// devuelve la story al backlog; el re-despacho la limpia. Se extrae aquí como
// función pura para poder testearla sin DOM (el runner es node:test, sin RTL).

import type { OrchestratorTicket } from "./types";

/** isAgentLost: la story lleva una nota de agente-perdido no vacía. */
export function isAgentLost(t: Pick<OrchestratorTicket, "agent_lost">): boolean {
  return typeof t.agent_lost === "string" && t.agent_lost.trim().length > 0;
}
