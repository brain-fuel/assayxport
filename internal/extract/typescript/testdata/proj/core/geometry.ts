// geometry.ts — cleanly typed, modern TS. The "matches up" baseline.

/** A point in 2D space. */
export interface Point {
  readonly x: number;
  readonly y: number;
}

/** Shapes the renderer understands. */
export type Shape = Circle | Rect;

export interface Circle {
  kind: "circle";
  center: Point;
  radius: number;
}

export interface Rect {
  kind: "rect";
  origin: Point;
  width: number;
  height: number;
}

export enum Unit {
  Pixels = "px",
  Points = "pt",
}

export function distance(a: Point, b: Point): number {
  const dx = a.x - b.x;
  const dy = a.y - b.y;
  return Math.sqrt(dx * dx + dy * dy);
}

export const area = (s: Shape): number => {
  switch (s.kind) {
    case "circle":
      return Math.PI * s.radius * s.radius;
    case "rect":
      return s.width * s.height;
  }
};

export class Path {
  private points: Point[] = [];

  add(p: Point): this {
    this.points.push(p);
    return this;
  }

  length(): number {
    let total = 0;
    for (let i = 1; i < this.points.length; i++) {
      total += distance(this.points[i - 1], this.points[i]);
    }
    return total;
  }
}
