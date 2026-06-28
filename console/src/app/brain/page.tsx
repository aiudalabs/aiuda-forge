import { Suspense } from "react";
import { BrainView } from "@/components/brain/BrainView";

export default function BrainPage() {
  return (
    <Suspense fallback={<div className="wrap">Cargando Brain…</div>}>
      <BrainView />
    </Suspense>
  );
}
