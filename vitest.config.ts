import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // A test that fails only on a loaded CI runner gets three more tries; locally it fails at once.
    retry: process.env['CI'] ? 3 : 0,
    include: ['tests/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.ts'],
      exclude: ['tests/**', 'src/index.ts'],
    },
  },
});
