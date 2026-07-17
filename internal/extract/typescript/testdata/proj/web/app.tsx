// app.tsx — TSX with a component, generics, async, and a couple of leaks.
import { area, Shape } from "../core/geometry";

export interface Props<T> {
  items: readonly T[];
  render: (item: T) => string;
}

export async function loadShapes(url: string): Promise<Shape[]> {
  const res = await fetch(url);
  return (await res.json()) as Shape[]; // assertion on external data
}

export function totalArea(shapes: Shape[]): number {
  return shapes.reduce((sum, s) => sum + area(s), 0);
}

export const Badge = (props: { label: string }) => {
  return <span className="badge">{props.label}</span>;
};

// @ts-nocheck would disable a whole file; here just an untyped export const.
export const registry = {};
