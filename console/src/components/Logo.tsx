// Marca Fluxo: "{ Fluxo }" (estilo código, el producto) sobre "by <aiuda/> labs" (quién
// lo hace). El sub-logo <aiuda/> labs mantiene su estilo (doc 15): `<` `/>` accent peso 400,
// `ai` accent 900, `uda` ink 900, `labs` gris 500.

export function AiudaLogo() {
  return (
    <span className="logo" aria-label="aiuda labs">
      <span className="b">&lt;</span>
      <span className="word">
        <span className="ai">ai</span>
        <span className="uda">uda</span>
      </span>
      <span className="b">/&gt;</span>
      <span className="labs">labs</span>
    </span>
  );
}

export function Logo({ size = "sm" }: { size?: "sm" | "lg" }) {
  return (
    <div className={`brand ${size}`} aria-label="Fluxo by aiuda labs">
      <div className="brand-name">
        <span className="brace">{"{"}</span>
        <span className="nm">Fluxo</span>
        <span className="brace">{"}"}</span>
      </div>
      <div className="brand-by">
        by <AiudaLogo />
      </div>
    </div>
  );
}
