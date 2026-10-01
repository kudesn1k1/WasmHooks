import type { ReactNode } from "react";

import { Badge } from "@/shared/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/shared/ui/card";

type PageStubProps = {
  title: string;
  description: string;
  milestone: string;
  children?: ReactNode;
};

export function PageStub({ title, description, milestone, children }: PageStubProps) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <Badge variant="outline">{milestone}</Badge>
      </div>
      <Card className="border-dashed">
        <CardHeader>
          <CardTitle>Экран в разработке</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        {children && <CardContent>{children}</CardContent>}
      </Card>
    </div>
  );
}
