export function formatQuota(value: number, unit: string): string {
  return value === 0 ? "по умолчанию" : `${value} ${unit}`;
}
