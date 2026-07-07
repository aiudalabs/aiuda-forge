// Marca Fluxo: "{ Fluxo }" (estilo código, el producto) sobre "by <aiuda/> labs" (quién
// lo hace). El sub-logo <aiuda/> labs mantiene su estilo (doc 15): `<` `/>` accent peso 400,
// `ai` accent 900, `uda` ink 900, `labs` gris 500.
// "{ Fluxo }" es un link al inicio; "<aiuda/> labs" abre aiudalabs.com en pestaña nueva.

import Link from "next/link";

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
      <Link href="/" className="brand-name" aria-label="Ir al inicio" style={{ cursor: "pointer" }}>
        <span className="brace">{"{"}</span>
        <span className="nm">Fluxo</span>
        <span className="brace">{"}"}</span>
      </Link>
      <div className="brand-by">
        <span>by</span>
        <a
          href="https://aiudalabs.com"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="aiudalabs.com (abre en pestaña nueva)"
          style={{ cursor: "pointer", display: "inline-flex" }}
        >
          <AiudaLogo />
        </a>
      </div>
    </div>
  );
}
