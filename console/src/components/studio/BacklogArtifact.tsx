"use client";

// Renderizador de artefacto: BACKLOG — tarjetas de historia legibles desde YAML.
// Usado por PhasePanel para los pasos "backlog"/"plan" (ver BACKLOG_STEPS).

import * as jsYaml from "js-yaml";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useT } from "@/lib/i18n";

interface BacklogStory {
  id?: string;
  title?: string;
  body?: string;
  acceptance?: string;
  owner?: string;
  deps?: string[];
}

interface BacklogYaml {
  epic?: {
    id?: string;
    title?: string;
    description?: string;
  };
  stories?: BacklogStory[];
}

export function BacklogArtifact({ raw }: { raw: string }) {
  const t = useT();
  let parsed: BacklogYaml | null = null;
  try {
    parsed = jsYaml.load(raw) as BacklogYaml;
  } catch {
    /* YAML inválido — caemos al markdown */
  }

  // Si el parse falla o el doc no tiene la forma esperada, renderizamos como markdown.
  if (!parsed || (!parsed.epic && !parsed.stories)) {
    return (
      <div className="artifact-md">
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{raw}</ReactMarkdown>
      </div>
    );
  }

  const { epic, stories = [] } = parsed;

  return (
    <div className="backlog-artifact">
      {epic && (
        <div className="backlog-epic">
          <div className="backlog-epic-id">{epic.id}</div>
          <h3 className="backlog-epic-title">{epic.title}</h3>
          {epic.description && (
            <p className="backlog-epic-desc">{epic.description}</p>
          )}
        </div>
      )}

      <div className="story-list">
        {stories.map((s, i) => (
          <div key={s.id ?? i} className="story-card">
            <div className="story-card-header">
              <span className="story-id">{s.id}</span>
              <span className="story-title">{s.title}</span>
            </div>
            <div className="story-card-meta">
              {s.owner && (
                <span className="story-chip owner">{s.owner}</span>
              )}
              {(s.deps ?? []).map((d) => (
                <span key={d} className="story-chip dep">
                  dep: {d}
                </span>
              ))}
            </div>
            {(s.body || s.acceptance) && (
              <div className="story-card-body">
                {s.body && (
                  <div className="artifact-md" style={{ padding: 0 }}>
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.body}</ReactMarkdown>
                  </div>
                )}
                {s.acceptance && (
                  <>
                    <div className="story-acceptance-label">{t("studio.view.acceptanceCriteria")}</div>
                    <div className="artifact-md" style={{ padding: 0 }}>
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>{s.acceptance}</ReactMarkdown>
                    </div>
                  </>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
