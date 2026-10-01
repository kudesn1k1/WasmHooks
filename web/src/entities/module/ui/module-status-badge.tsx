import { Badge } from "@/shared/ui/badge";

import type { ModuleStatus } from "../model/schema";

const labels: Record<ModuleStatus, string> = {
  uploaded: "загружен",
  validating: "проверяется",
  validated: "проверен",
  rejected: "отклонён",
  archived: "в архиве",
};

const variants: Record<ModuleStatus, "default" | "secondary" | "destructive" | "outline"> = {
  uploaded: "outline",
  validating: "outline",
  validated: "secondary",
  rejected: "destructive",
  archived: "outline",
};

export function ModuleStatusBadge({ status }: { status: ModuleStatus }) {
  return <Badge variant={variants[status]}>{labels[status]}</Badge>;
}
