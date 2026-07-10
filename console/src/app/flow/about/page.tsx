"use client";

// /flow/about — "Qué es cada estación": la tabla estación → término Scrum → qué hace
// Fluxo, marcando qué es estándar Scrum (Scrum Guide de Schwaber & Sutherland) y qué
// es propio de Fluxo (los ◆ gates humanos, el QA visual, el refinamiento ejecutado por
// agente). Breve por diseño: una tabla + tres párrafos, no un manual. Contenido
// embebido por idioma (página de contenido, self-contained) vía useI18n().lang.

import Link from "next/link";
import { useI18n, type Lang } from "@/lib/i18n";

const SCRUM_GUIDE = "https://scrumguides.org";

interface Row {
  station: string;
  scrum: string; // término de la Scrum Guide
  fluxo: string; // qué hace Fluxo
  origin: "scrum" | "fluxo"; // estándar Scrum vs propio de Fluxo
}

interface AboutContent {
  title: string;
  intro: string;
  back: string;
  colStation: string;
  colScrum: string;
  colFluxo: string;
  colOrigin: string;
  tagScrum: string;
  tagFluxo: string;
  rows: Row[];
  paras: string[]; // exactamente 3
  guideNote: string;
}

const CONTENT: Record<Lang, AboutContent> = {
  es: {
    title: "El ciclo — qué es cada estación",
    intro: "Fluxo ejecuta Scrum. Cada estación del ciclo es una ceremonia o un artefacto del marco; abajo, su nombre canónico y qué hace Fluxo en ese punto.",
    back: "← Volver al ciclo",
    colStation: "Estación",
    colScrum: "Término Scrum",
    colFluxo: "Qué hace Fluxo",
    colOrigin: "Origen",
    tagScrum: "Scrum",
    tagFluxo: "◆ propio",
    rows: [
      { station: "Studio / Diseño", scrum: "—", fluxo: "El pipeline que va de la idea al backlog inicial (brief → PRD → arquitectura → UI → backlog), con tu aprobación por fase.", origin: "fluxo" },
      { station: "Product Backlog", scrum: "Product Backlog", fluxo: "La lista priorizada de todo lo que el producto necesita. Le añades trabajo con “＋ añadir al Product Backlog”.", origin: "scrum" },
      { station: "Sprint Planning", scrum: "Sprint Planning", fluxo: "Se arma el plan del sprint; tú lo apruebas (◆ gate humano) antes de arrancar.", origin: "scrum" },
      { station: "Sprint Backlog", scrum: "Sprint Backlog", fluxo: "Las historias comprometidas para el sprint, listas según sus dependencias.", origin: "scrum" },
      { station: "Sprint", scrum: "Sprint", fluxo: "La iteración donde los agentes implementan: spec dev-ready → tests → revisión → verificación → PR.", origin: "scrum" },
      { station: "Daily", scrum: "Daily Scrum", fluxo: "La sincronización diaria; en Fluxo, un resumen diario a tu canal.", origin: "scrum" },
      { station: "Increment", scrum: "Increment", fluxo: "El producto incrementado e integrable, con preview navegable.", origin: "scrum" },
      { station: "Sprint Review", scrum: "Sprint Review", fluxo: "Se inspecciona el incremento; Fluxo entrega un reporte con evidencia y tú aceptas, o tu feedback se vuelve historias.", origin: "scrum" },
      { station: "Sprint Retrospective", scrum: "Sprint Retrospective", fluxo: "Se mejora el proceso; un agente analiza el sprint y propone mejoras al método.", origin: "scrum" },
    ],
    paras: [
      "Las ceremonias y artefactos son los canónicos de Scrum: Product Backlog, Sprint Planning, Sprint Backlog, Sprint, Daily, Increment, Sprint Review y Sprint Retrospective, tal como los define la Scrum Guide.",
      "Lo propio de Fluxo son tres cosas: los ◆ gates humanos (apruebas, respondes o rechazas en cada fase), el QA visual del incremento contra el mockup, y el refinamiento y la retrospectiva ejecutados por agentes con evidencia real del sprint.",
      "Antes del primer sprint, el pipeline de diseño (Studio) lleva tu idea de vago a un Product Backlog inicial. Ese paso —de la idea al backlog— es la extensión de Fluxo al marco: Scrum asume un Product Backlog ya existente.",
    ],
    guideNote: "Referencia canónica: la Scrum Guide de Ken Schwaber y Jeff Sutherland.",
  },
  en: {
    title: "The cycle — what each station is",
    intro: "Fluxo runs Scrum. Each station of the cycle is a ceremony or an artifact of the framework; below, its canonical name and what Fluxo does at that point.",
    back: "← Back to the cycle",
    colStation: "Station",
    colScrum: "Scrum term",
    colFluxo: "What Fluxo does",
    colOrigin: "Origin",
    tagScrum: "Scrum",
    tagFluxo: "◆ Fluxo",
    rows: [
      { station: "Studio / Design", scrum: "—", fluxo: "The pipeline from idea to the initial backlog (brief → PRD → architecture → UI → backlog), with your approval per phase.", origin: "fluxo" },
      { station: "Product Backlog", scrum: "Product Backlog", fluxo: "The prioritized list of everything the product needs. You add work with “＋ add to Product Backlog”.", origin: "scrum" },
      { station: "Sprint Planning", scrum: "Sprint Planning", fluxo: "The sprint plan is drawn up; you approve it (◆ human gate) before it starts.", origin: "scrum" },
      { station: "Sprint Backlog", scrum: "Sprint Backlog", fluxo: "The stories committed for the sprint, ready by their dependencies.", origin: "scrum" },
      { station: "Sprint", scrum: "Sprint", fluxo: "The iteration where agents implement: dev-ready spec → tests → review → verification → PR.", origin: "scrum" },
      { station: "Daily", scrum: "Daily Scrum", fluxo: "The daily sync; in Fluxo, a daily summary to your channel.", origin: "scrum" },
      { station: "Increment", scrum: "Increment", fluxo: "The incremented, integrable product, with a browsable preview.", origin: "scrum" },
      { station: "Sprint Review", scrum: "Sprint Review", fluxo: "The increment is inspected; Fluxo delivers a report with evidence and you accept, or your feedback becomes stories.", origin: "scrum" },
      { station: "Sprint Retrospective", scrum: "Sprint Retrospective", fluxo: "The process is improved; an agent analyzes the sprint and proposes improvements to the method.", origin: "scrum" },
    ],
    paras: [
      "The ceremonies and artifacts are the canonical ones of Scrum: Product Backlog, Sprint Planning, Sprint Backlog, Sprint, Daily, Increment, Sprint Review, and Sprint Retrospective, exactly as the Scrum Guide defines them.",
      "What's specific to Fluxo is three things: the ◆ human gates (you approve, answer, or reject at each phase), the visual QA of the increment against the mockup, and the refinement and retrospective executed by agents with real evidence from the sprint.",
      "Before the first sprint, the design pipeline (Studio) takes your idea from vague to an initial Product Backlog. That step —from idea to backlog— is Fluxo's extension to the framework: Scrum assumes a Product Backlog already exists.",
    ],
    guideNote: "Canonical reference: the Scrum Guide by Ken Schwaber and Jeff Sutherland.",
  },
  pt: {
    title: "O ciclo — o que é cada estação",
    intro: "O Fluxo executa Scrum. Cada estação do ciclo é uma cerimônia ou um artefato do framework; abaixo, seu nome canônico e o que o Fluxo faz nesse ponto.",
    back: "← Voltar ao ciclo",
    colStation: "Estação",
    colScrum: "Termo Scrum",
    colFluxo: "O que o Fluxo faz",
    colOrigin: "Origem",
    tagScrum: "Scrum",
    tagFluxo: "◆ próprio",
    rows: [
      { station: "Studio / Design", scrum: "—", fluxo: "O pipeline que vai da ideia ao backlog inicial (brief → PRD → arquitetura → UI → backlog), com sua aprovação por fase.", origin: "fluxo" },
      { station: "Product Backlog", scrum: "Product Backlog", fluxo: "A lista priorizada de tudo o que o produto precisa. Você adiciona trabalho com “＋ adicionar ao Product Backlog”.", origin: "scrum" },
      { station: "Sprint Planning", scrum: "Sprint Planning", fluxo: "O plano do sprint é montado; você o aprova (◆ gate humano) antes de começar.", origin: "scrum" },
      { station: "Sprint Backlog", scrum: "Sprint Backlog", fluxo: "As histórias comprometidas para o sprint, prontas segundo suas dependências.", origin: "scrum" },
      { station: "Sprint", scrum: "Sprint", fluxo: "A iteração onde os agentes implementam: spec dev-ready → testes → revisão → verificação → PR.", origin: "scrum" },
      { station: "Daily", scrum: "Daily Scrum", fluxo: "A sincronização diária; no Fluxo, um resumo diário no seu canal.", origin: "scrum" },
      { station: "Increment", scrum: "Increment", fluxo: "O produto incrementado e integrável, com preview navegável.", origin: "scrum" },
      { station: "Sprint Review", scrum: "Sprint Review", fluxo: "O incremento é inspecionado; o Fluxo entrega um relatório com evidência e você aceita, ou seu feedback vira histórias.", origin: "scrum" },
      { station: "Sprint Retrospective", scrum: "Sprint Retrospective", fluxo: "O processo é melhorado; um agente analisa o sprint e propõe melhorias ao método.", origin: "scrum" },
    ],
    paras: [
      "As cerimônias e artefatos são os canônicos do Scrum: Product Backlog, Sprint Planning, Sprint Backlog, Sprint, Daily, Increment, Sprint Review e Sprint Retrospective, exatamente como a Scrum Guide os define.",
      "O que é próprio do Fluxo são três coisas: os ◆ gates humanos (você aprova, responde ou rejeita em cada fase), o QA visual do incremento contra o mockup, e o refinamento e a retrospectiva executados por agentes com evidência real do sprint.",
      "Antes do primeiro sprint, o pipeline de design (Studio) leva sua ideia do vago a um Product Backlog inicial. Esse passo —da ideia ao backlog— é a extensão do Fluxo ao framework: o Scrum assume um Product Backlog já existente.",
    ],
    guideNote: "Referência canônica: a Scrum Guide de Ken Schwaber e Jeff Sutherland.",
  },
};

export default function FlowAboutPage() {
  const { lang } = useI18n();
  const c = CONTENT[lang] ?? CONTENT.es;
  return (
    <div className="about-wrap">
      <div className="about-head">
        <Link href="/flow" className="about-back">
          {c.back}
        </Link>
        <h1>{c.title}</h1>
        <p className="about-intro">{c.intro}</p>
      </div>

      <div className="about-tablewrap">
        <table className="about-table">
          <thead>
            <tr>
              <th>{c.colStation}</th>
              <th>{c.colScrum}</th>
              <th>{c.colFluxo}</th>
              <th>{c.colOrigin}</th>
            </tr>
          </thead>
          <tbody>
            {c.rows.map((r) => (
              <tr key={r.station}>
                <td className="about-station">{r.station}</td>
                <td className="about-scrum">{r.scrum}</td>
                <td>{r.fluxo}</td>
                <td>
                  <span className={`about-tag ${r.origin}`}>
                    {r.origin === "scrum" ? c.tagScrum : c.tagFluxo}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="about-prose">
        {c.paras.map((p, i) => (
          <p key={i}>{p}</p>
        ))}
        <p className="about-guide">
          {c.guideNote}{" "}
          <a href={SCRUM_GUIDE} target="_blank" rel="noreferrer">
            scrumguides.org →
          </a>
        </p>
      </div>

      <style jsx>{`
        .about-wrap {
          max-width: 940px;
          margin: 0 auto;
          padding: 28px 20px 60px;
        }
        .about-back {
          display: inline-block;
          font-size: 13px;
          font-weight: 600;
          color: var(--accent, #e8440a);
          text-decoration: none;
          margin-bottom: 14px;
        }
        .about-back:hover {
          text-decoration: underline;
        }
        .about-head h1 {
          font-size: 26px;
          font-weight: 900;
          letter-spacing: -0.4px;
          margin: 0 0 8px;
        }
        .about-intro {
          font-size: 14px;
          color: var(--ink2, #444);
          max-width: 720px;
          line-height: 1.5;
          margin: 0 0 22px;
        }
        .about-tablewrap {
          overflow-x: auto;
          border: 1px solid var(--stroke, #e6e1d8);
          border-radius: 12px;
        }
        .about-table {
          width: 100%;
          border-collapse: collapse;
          font-size: 13px;
          min-width: 640px;
        }
        .about-table th {
          text-align: left;
          font-weight: 800;
          font-size: 11px;
          text-transform: uppercase;
          letter-spacing: 0.4px;
          color: var(--ink4, #77726a);
          padding: 10px 14px;
          background: var(--bg2, #faf8f4);
          border-bottom: 1px solid var(--stroke, #e6e1d8);
        }
        .about-table td {
          padding: 11px 14px;
          border-bottom: 1px solid var(--line, #efeae1);
          vertical-align: top;
          line-height: 1.45;
          color: var(--ink, #1a1712);
        }
        .about-table tr:last-child td {
          border-bottom: none;
        }
        .about-station {
          font-weight: 800;
          white-space: nowrap;
        }
        .about-scrum {
          font-weight: 600;
          color: var(--ink2, #444);
          white-space: nowrap;
        }
        .about-tag {
          display: inline-block;
          font-size: 11px;
          font-weight: 800;
          padding: 2px 8px;
          border-radius: 999px;
          white-space: nowrap;
        }
        .about-tag.scrum {
          background: #e8f0fd;
          color: #1660c9;
        }
        .about-tag.fluxo {
          background: #fdede5;
          color: #e8440a;
        }
        .about-prose {
          margin-top: 24px;
          max-width: 720px;
        }
        .about-prose p {
          font-size: 14px;
          line-height: 1.6;
          color: var(--ink2, #333);
          margin: 0 0 14px;
        }
        .about-guide {
          font-size: 12.5px !important;
          color: var(--ink4, #77726a) !important;
        }
        .about-guide a {
          color: var(--accent, #e8440a);
          font-weight: 600;
          text-decoration: none;
        }
        .about-guide a:hover {
          text-decoration: underline;
        }
      `}</style>
    </div>
  );
}
