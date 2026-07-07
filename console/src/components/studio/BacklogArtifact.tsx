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
  sprint_id?: string;
}

interface BacklogSprint {
  id?: string;
  name?: string;
  goal?: string;
}

interface BacklogYaml {
  epic?: {
    id?: string;
    title?: string;
    description?: string;
  };
  sprints?: BacklogSprint[];
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

  const { epic, sprints = [], stories = [] } = parsed;

  // Agrupamos las historias por sprint (en el ORDEN de `sprints`), para que se
  // distingan los sprints con su meta. Las historias sin sprint conocido caen a un
  // grupo final "Sin sprint". Sin `sprints`, es una sola lista plana (compat).
  const bySprint = new Map<string, BacklogStory[]>();
  for (const s of stories) {
    const key = s.sprint_id ?? "";
    if (!bySprint.has(key)) bySprint.set(key, []);
    bySprint.get(key)!.push(s);
  }
  const orderedGroups: { sprint: BacklogSprint | null; stories: BacklogStory[] }[] = [];
  if (sprints.length) {
    for (const sp of sprints) {
      const list = bySprint.get(sp.id ?? "") ?? [];
      if (list.length) orderedGroups.push({ sprint: sp, stories: list });
    }
    const orphan = bySprint.get("") ?? [];
    if (orphan.length) orderedGroups.push({ sprint: null, stories: orphan });
  } else {
    orderedGroups.push({ sprint: null, stories });
  }

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

      {orderedGroups.map((g, gi) => (
        <div key={g.sprint?.id ?? `g${gi}`} className="backlog-sprint">
          {g.sprint && (
            <div className="backlog-sprint-head">
              <div className="backlog-sprint-title">
                <span className="backlog-sprint-id">{g.sprint.id}</span>
                <span className="backlog-sprint-name">{g.sprint.name}</span>
                <span className="backlog-sprint-count">{g.stories.length}</span>
              </div>
              {g.sprint.goal && <p className="backlog-sprint-goal">{g.sprint.goal}</p>}
            </div>
          )}
          <div className="story-list">
            {g.stories.map((s, i) => (
              <div key={s.id ?? i} className="story-card">
                <div className="story-card-header">
                  <span className="story-id">{s.id}</span>
                  <span className="story-title">{s.title}</span>
                </div>
                <div className="story-card-meta">
                  {s.owner && <span className="story-chip owner">{s.owner}</span>}
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
      ))}
    </div>
  );
}
