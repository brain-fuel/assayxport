// util.js — plain JavaScript in a TS project: an entirely untyped module.
// Everything here "works" but has no type contract to match up with.
export function clamp(v, lo, hi) {
  if (v < lo) return lo;
  if (v > hi) return hi;
  return v;
}

export var LEGACY_MODE = true; // var + no type

export function equalsLoose(a, b) {
  return a == b; // loose equality
}
