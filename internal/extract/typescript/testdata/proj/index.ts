// index.ts — package entrypoint that re-exports and has a main.
export * from "./core/geometry";
export { midpoint } from "./legacy/loose";

import { Path } from "./core/geometry";

export function main(): void {
  const p = new Path();
  console.log(p.length());
}

main();
