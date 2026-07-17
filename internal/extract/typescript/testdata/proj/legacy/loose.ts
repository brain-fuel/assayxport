// loose.ts — compiles and runs, but does NOT "match up": escape hatches and
// untyped surfaces the flagger should catch.
import { distance, Point } from "../core/geometry";

// implicit any parameter (no type) — works, doesn't match up.
export function midpoint(a, b) {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

// explicit any — defeats the type system.
export function parse(raw: any): any {
  return JSON.parse(raw);
}

// `as any` assertion laundering a value through the type system.
export function forcePoint(v: unknown): Point {
  return v as any;
}

// non-null assertion — asserts away a possible undefined.
export function firstX(pts: Point[]): number {
  return pts[0]!.x;
}

// @ts-ignore suppresses a real type error on the next line.
export function bad(p: Point): number {
  // @ts-ignore
  return distance(p, "not a point");
}

// double assertion through unknown.
export function reinterpret(n: number): string {
  return n as unknown as string;
}
