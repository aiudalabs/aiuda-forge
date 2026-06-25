// Logo de marca: <aiuda/> labs — `<` `/>` en accent peso 400, `ai` accent peso 900,
// `uda` ink peso 900, `labs` gris peso 500 (doc 15).

export function Logo() {
  return (
    <div className="logo" aria-label="aiuda labs">
      <span className="b">&lt;</span>
      <span className="word">
        <span className="ai">ai</span>
        <span className="uda">uda</span>
      </span>
      <span className="b">/&gt;</span>
      <span className="labs">labs</span>
    </div>
  );
}
