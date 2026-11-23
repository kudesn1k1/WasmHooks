import { Amphora, Bean, Coffee, Flame, Flower2, Leaf, Store, type LucideIcon } from "lucide-react";

import { cn } from "@/shared/lib/utils";

const tones = {
  tea: "bg-[#eaf0e4] text-[#3f5c36] dark:bg-green-950 dark:text-green-200",
  coffee: "bg-[#f3e6dd] text-[#8b5636] dark:bg-orange-950 dark:text-orange-200",
  glass: "bg-[#e9eff3] text-[#526e83] dark:bg-sky-950 dark:text-sky-200",
  honey: "bg-[#fbf0d9] text-[#946814] dark:bg-amber-950 dark:text-amber-200",
  neutral: "bg-muted text-muted-foreground",
};

type Art = { icon: LucideIcon; tone: keyof typeof tones };

const storefrontArt = new Map<string, Art>([
  ["merchant-a", { icon: Leaf, tone: "tea" }],
  ["merchant-b", { icon: Coffee, tone: "coffee" }],
  ["tea-sencha", { icon: Leaf, tone: "tea" }],
  ["teapot-glass", { icon: Amphora, tone: "glass" }],
  ["honey-linden", { icon: Flower2, tone: "honey" }],
  ["coffee-ethiopia", { icon: Bean, tone: "coffee" }],
  ["cezve-copper", { icon: Flame, tone: "coffee" }],
  ["mug-ceramic", { icon: Coffee, tone: "coffee" }],
]);

const defaultArt: Art = { icon: Store, tone: "neutral" };

function artFor(id: string) {
  return storefrontArt.get(id) ?? defaultArt;
}

export function shopTone(id: string) {
  return tones[artFor(id).tone];
}

export function ShopArt({ id, className }: { id: string; className?: string }) {
  const { icon: Icon } = artFor(id);

  return (
    <span aria-hidden="true" className={cn("grid size-20 shrink-0 place-items-center rounded-xl", shopTone(id), className)}>
      <Icon className="size-[36%]" strokeWidth={1.5} />
    </span>
  );
}
