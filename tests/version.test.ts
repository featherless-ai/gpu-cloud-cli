import { describe, expect, it } from 'vitest';
import { formatDollars, isNewer } from '../src/version';

describe('isNewer', () => {
  it('compares patch, minor, and major', () => {
    expect(isNewer('0.1.1', '0.1.0')).toBe(true);
    expect(isNewer('0.2.0', '0.1.9')).toBe(true);
    expect(isNewer('1.0.0', '0.9.9')).toBe(true);
    expect(isNewer('0.1.0', '0.1.0')).toBe(false);
    expect(isNewer('0.0.9', '0.1.0')).toBe(false);
  });
});

describe('formatDollars', () => {
  it('formats cent-and-above amounts with two decimals', () => {
    expect(formatDollars('2500000')).toBe('$2.50');
  });

  it('keeps full precision for sub-cent amounts', () => {
    expect(formatDollars('123')).toBe('$0.000123');
  });

  it('renders zero as a dollar amount', () => {
    expect(formatDollars('0')).toBe('$0.00');
  });
});
