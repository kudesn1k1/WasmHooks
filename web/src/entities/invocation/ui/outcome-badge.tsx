import { Badge } from "@/shared/ui/badge";

import type { Outcome } from "../model/schema";

const variants: Record<Outcome, "default" | "secondary" | "destructive" | "outline"> = {
  ok: "secondary",
  no_handler: "outline",
  timeout: "destructive",
  handler_error: "destructive",
  quota_exceeded: "outline",
  unavailable: "outline",
};

export function OutcomeBadge({ outcome }: { outcome: Outcome }) {
  return (
    <Badge variant={variants[outcome]} className="font-mono">
      {outcome}
    </Badge>
  );
}
