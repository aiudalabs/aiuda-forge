// Diff propuesto — se revisa ANTES de aprobar (doc 16 §10). Colorea líneas +/-/hunk.

export function DiffBox({ diff, style }: { diff: string; style?: React.CSSProperties }) {
  const lines = diff.split("\n");
  return (
    <div className="diffbox" style={style}>
      {lines.map((line, i) => {
        let cls = "";
        if (line.startsWith("+++") || line.startsWith("---") || line.startsWith("@@")) cls = "hh";
        else if (line.startsWith("+")) cls = "add";
        else if (line.startsWith("-")) cls = "del";
        return (
          <div key={i} className={cls}>
            {line || " "}
          </div>
        );
      })}
    </div>
  );
}
