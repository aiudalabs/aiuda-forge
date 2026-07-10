import { test } from "node:test";
import assert from "node:assert/strict";

import { isAgentLost } from "./agentLost.ts";

// El badge "agente perdido" (card del Kanban) y el banner + botón de recuperación
// (TicketDetail) se muestran ⟺ isAgentLost() es true. Sin DOM (runner node:test,
// sin RTL) se testea la decisión pura que gobierna esa condición de render.

test("una story con nota agent_lost muestra el badge/botón", () => {
  assert.equal(isAgentLost({ agent_lost: "Copilot agent task perdida: abc-123" }), true);
});

test("una story sin nota NO muestra el badge/botón", () => {
  assert.equal(isAgentLost({}), false);
  assert.equal(isAgentLost({ agent_lost: undefined }), false);
  assert.equal(isAgentLost({ agent_lost: "" }), false);
});

test("una nota en blanco no cuenta (no badge fantasma)", () => {
  assert.equal(isAgentLost({ agent_lost: "   " }), false);
});
