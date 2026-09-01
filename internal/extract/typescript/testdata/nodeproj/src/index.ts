export function greet(name: string): string { return "hi " + name; }

export class Vault {
  #combo = 3;
  private open(): void {}
  protected peek(): number { return this.#combo; }
  privateer(): void {}
  render(): string { return "x"; }
}

const impl = { greet };
export default impl;
