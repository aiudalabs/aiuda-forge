import { Studio } from "@/components/studio/Studio";

// Studio lives at /studio (like every other section). With an active project it shows
// the Especificación (docs + inline pipeline); with none it bounces to the home entry.
export default function StudioPage() {
  return <Studio />;
}
