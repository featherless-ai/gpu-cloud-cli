import pkg from '../package.json' with { type: 'json' };

export const APP_NAME = 'featherless-cloud';

export const PACKAGE_NAME = pkg.name;
export const VERSION = pkg.version;

export function isNewer(candidate: string, current: string): boolean {
  const a = candidate.split('.').map(Number);
  const b = current.split('.').map(Number);

  for (let i = 0; i < 3; i++) {
    const x = a[i] ?? 0;
    const y = b[i] ?? 0;

    if (x !== y) {
      return x > y;
    }
  }

  return false;
}

/** Human-readable dollars; sub-cent amounts keep full micro precision. Never micro units. */
export function formatDollars(micros: string | number): string {
  const m = Number(micros);

  if (m >= 10_000) {
    return `$${(m / 1e6).toFixed(2)}`;
  }

  const dollars = (m / 1e6).toFixed(6).replace(/0+$/, '').replace(/\.$/, '.00');
  return `$${dollars}`;
}
