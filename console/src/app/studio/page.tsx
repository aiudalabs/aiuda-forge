import { redirect } from "next/navigation";

// Studio vive en "/" — redirigir para no tener dos URLs para lo mismo.
export default function StudioPage() {
  redirect("/");
}
