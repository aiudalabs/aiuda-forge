import { redirect } from "next/navigation";

// El Board de runs era la vista del ejecutor factory legacy (apagado por
// default desde F4). La ejecución vive en GitHub; su vista es /agents.
export default function BoardPage() {
  redirect("/agents");
}
