"use client";

// EQUIPO — miembros e invitaciones del proyecto activo (v1.2 roles).
//  · Cualquier miembro ve la lista (owner·editor·viewer).
//  · Sólo el OWNER ve los controles de gestión (invitar, cambiar rol, quitar).
// El rol propio se deriva cruzando el email de /auth/me con la lista de miembros;
// el owner del proyecto está protegido (no se degrada ni se quita).

import { useMemo, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "@/lib/api";
import { me } from "@/lib/auth";
import { useActiveProject } from "@/lib/activeProject";
import { useT } from "@/lib/i18n";
import type { Role } from "@/lib/types";

export function MembersView() {
  const { activeId, project } = useActiveProject();
  const qc = useQueryClient();
  const t = useT();

  const meQuery = useQuery({ queryKey: ["me"], queryFn: () => me() });
  const membersQuery = useQuery({
    queryKey: ["members", activeId],
    queryFn: () => api.listMembers(activeId as string),
    enabled: !!activeId,
  });

  const myEmail = meQuery.data?.email ?? "";
  const myId = meQuery.data?.id;
  const members = useMemo(() => membersQuery.data?.members ?? [], [membersQuery.data]);
  const invites = membersQuery.data?.invites ?? [];

  // Mi rol en este proyecto: el del member con mi email (o owner si soy owner_id).
  const myRole: Role | "" = useMemo(() => {
    const mine = members.find((m) => m.email === myEmail);
    if (mine) return mine.role;
    if (project?.owner_id && project.owner_id === myId) return "owner";
    return "";
  }, [members, myEmail, project, myId]);

  const isOwner = myRole === "owner";

  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("editor");
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => qc.invalidateQueries({ queryKey: ["members", activeId] });

  const invite = useMutation({
    mutationFn: () => api.inviteMember(activeId as string, email, role),
    onSuccess: (res) => {
      setError(null);
      setEmail("");
      if (res.status === "invited" && res.invite_path) {
        const link = `${window.location.origin}${res.invite_path}`;
        navigator.clipboard?.writeText(link).catch(() => {});
        setNotice(`${t("team.inviteCreated")}: ${link}`);
      } else {
        setNotice(`${res.email} ${t("team.added")} ${roleLabel(res.role, t)}.`);
      }
      refresh();
    },
    onError: (e) => setError(e instanceof Error ? e.message : t("team.cannotInvite")),
  });

  const changeRole = useMutation({
    mutationFn: ({ userId, r }: { userId: string; r: Role }) =>
      api.updateMemberRole(activeId as string, userId, r),
    onSuccess: refresh,
    onError: (e) => setError(e instanceof Error ? e.message : t("team.cannotChangeRole")),
  });

  const remove = useMutation({
    mutationFn: (userId: string) => api.removeMember(activeId as string, userId),
    onSuccess: refresh,
    onError: (e) => setError(e instanceof Error ? e.message : t("team.cannotRemove")),
  });

  if (!activeId) {
    return (
      <div className="mwrap">
        <div className="placeholder">{t("team.selectProject")}</div>
        <style jsx>{styles}</style>
      </div>
    );
  }

  return (
    <div className="mwrap">
      <div className="sectitle">
        <h2>{t("team.title")}</h2>
        <span className="c">{t("team.subtitle")} · {project?.name ?? activeId}</span>
      </div>

      {!isOwner && (
        <p className="hint">
          {t("team.yourRole")}: <strong>{roleLabel(myRole || "viewer", t)}</strong>.{" "}
          {t("team.ownerOnly")}
        </p>
      )}

      {isOwner && (
        <section className="card">
          <h3>{t("team.invite")}</h3>
          <div className="inviteRow">
            <input
              type="email"
              placeholder="email@ejemplo.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="inp"
            />
            <select value={role} onChange={(e) => setRole(e.target.value as Role)} className="inp sel">
              <option value="editor">{t("role.editor")}</option>
              <option value="viewer">{t("role.viewer")}</option>
            </select>
            <button
              className="btn"
              disabled={!email || invite.isPending}
              onClick={() => invite.mutate()}
            >
              {invite.isPending ? t("team.inviting") : t("team.invite")}
            </button>
          </div>
          <p className="micro">{t("team.inviteHint")}</p>
        </section>
      )}

      {notice && <div className="notice ok">{notice}</div>}
      {error && <div className="notice err">{error}</div>}

      <section className="card">
        <h3>{t("team.members")} ({members.length})</h3>
        {membersQuery.isLoading ? (
          <div className="placeholder">{t("common.loading")}</div>
        ) : (
          <ul className="list">
            {members.map((m) => {
              const isProjectOwner = m.role === "owner";
              return (
                <li key={m.user_id} className="row">
                  <span className="email">{m.email || m.user_id}</span>
                  {isOwner && !isProjectOwner ? (
                    <select
                      value={m.role}
                      onChange={(e) => changeRole.mutate({ userId: m.user_id, r: e.target.value as Role })}
                      className="inp sel small"
                    >
                      <option value="editor">{t("role.editor")}</option>
                      <option value="viewer">{t("role.viewer")}</option>
                    </select>
                  ) : (
                    <span className={`badge ${m.role}`}>{roleLabel(m.role, t)}</span>
                  )}
                  {isOwner && !isProjectOwner && (
                    <button className="link danger" onClick={() => remove.mutate(m.user_id)}>
                      {t("team.remove")}
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {invites.length > 0 && (
        <section className="card">
          <h3>{t("team.pendingInvites")} ({invites.length})</h3>
          <ul className="list">
            {invites.map((inv) => (
              <li key={inv.token} className="row">
                <span className="email">{inv.email}</span>
                <span className={`badge ${inv.role}`}>{roleLabel(inv.role, t)}</span>
                {isOwner && (
                  <button
                    className="link"
                    onClick={() => {
                      const link = `${window.location.origin}/invite/${inv.token}`;
                      navigator.clipboard?.writeText(link).catch(() => {});
                      setNotice(`${t("team.linkCopied")}: ${link}`);
                    }}
                  >
                    {t("team.copyLink")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}

      <style jsx>{styles}</style>
    </div>
  );
}

function roleLabel(r: Role, t: (key: string) => string): string {
  return t(`role.${r}`);
}

const styles = `
  .mwrap { width: 100%; display: flex; flex-direction: column; gap: 16px; }
  .sectitle h2 { margin: 0; font-weight: 900; font-size: 22px; }
  .sectitle .c { color: #888; font-size: 13px; }
  .hint { color: #666; font-size: 13px; margin: 0; }
  .card { background: #fff; border: 1px solid #eee; border-radius: 14px; padding: 18px; }
  .card h3 { margin: 0 0 12px; font-size: 14px; font-weight: 800; }
  .inviteRow { display: flex; gap: 8px; align-items: center; }
  .inp { box-sizing: border-box; padding: 9px 11px; border: 1px solid #ddd; border-radius: 9px; font-size: 14px; outline: none; }
  .inp.sel { background: #fff; }
  .inp.small { padding: 5px 8px; font-size: 13px; }
  /* email crece y ocupa el espacio; select ancho fijo; botón fijo */
  .inviteRow input.inp { flex: 1 1 auto; min-width: 200px; }
  .inviteRow select.sel { flex: 0 0 120px; width: 120px; }
  .inviteRow .btn { flex: 0 0 auto; }
  .btn { padding: 9px 16px; border: none; border-radius: 9px; background: #E8440A; color: #fff; font-weight: 700; cursor: pointer; }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .micro { color: #999; font-size: 12px; margin: 8px 0 0; }
  .notice { padding: 10px 14px; border-radius: 10px; font-size: 13px; }
  .notice.ok { background: #f0f8f0; color: #2a6; border: 1px solid #cce8cc; word-break: break-all; }
  .notice.err { background: #fdf0ec; color: #E8440A; border: 1px solid #f5d5c8; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 8px 0; border-bottom: 1px solid #f4f4f4; }
  .row:last-child { border-bottom: none; }
  .email { flex: 1; font-size: 14px; }
  .badge { font-size: 12px; font-weight: 700; padding: 3px 9px; border-radius: 999px; }
  .badge.owner { background: #fde8df; color: #E8440A; }
  .badge.editor { background: #e7f0fd; color: #2563c0; }
  .badge.viewer { background: #eee; color: #666; }
  .link { background: none; border: none; color: #2563c0; font-size: 13px; cursor: pointer; padding: 0; }
  .link.danger { color: #c0392b; }
  .placeholder { color: #888; padding: 16px 0; }
`;
