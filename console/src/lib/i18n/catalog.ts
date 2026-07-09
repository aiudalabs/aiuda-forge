// Catálogo i18n: compone los namespaces en un diccionario plano por idioma. Cada
// namespace exporta { es, en, pt } con claves ya prefijadas ("nav.board", etc.); aquí
// los fusionamos. Añadir un namespace = importarlo y sumarlo a `parts`.

import type { Lang } from "./index";
import { nav } from "./ns/nav";
import { common } from "./ns/common";
import { board } from "./ns/board";
import { tickets } from "./ns/tickets";
import { agents } from "./ns/agents";
import { studio } from "./ns/studio";
import { registry } from "./ns/registry";
import { spend } from "./ns/spend";
import { docs } from "./ns/docs";
import { brain } from "./ns/brain";
import { overview } from "./ns/overview";
import { settings } from "./ns/settings";
import { flow } from "./ns/flow";

type Bundle = { es: Record<string, string>; en: Record<string, string>; pt: Record<string, string> };

const parts: Bundle[] = [nav, common, board, tickets, agents, studio, registry, spend, docs, brain, overview, settings, flow];

function compose(lang: Lang): Record<string, string> {
  return Object.assign({}, ...parts.map((p) => p[lang]));
}

export const MESSAGES: Record<Lang, Record<string, string>> = {
  es: compose("es"),
  en: compose("en"),
  pt: compose("pt"),
};
