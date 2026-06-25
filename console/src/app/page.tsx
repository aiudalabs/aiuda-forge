import { Suspense } from "react";
import { BoardView } from "@/components/board/BoardView";

export default function BoardPage() {
  return (
    <Suspense fallback={<div className="wrap">Cargando board…</div>}>
      <BoardView />
    </Suspense>
  );
}
