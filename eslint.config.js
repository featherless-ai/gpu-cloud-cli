import js from '@eslint/js';
import tseslint from 'typescript-eslint';

const sharedRules = {
  'indent': ['error', 2],
  'quotes': ['error', 'single'],
  'semi': ['error', 'always'],
  'curly': ['error', 'all'],
  'brace-style': ['error', '1tbs', { allowSingleLine: false }],
  'max-statements-per-line': ['error', { max: 1 }],
  'max-params': ['error', 5],
  'key-spacing': ['error', { beforeColon: false, afterColon: true }],
  'object-curly-newline': ['error', { minProperties: 5, multiline: true, consistent: true }],
  'object-property-newline': ['error', { allowAllPropertiesOnSameLine: true }],
  'comma-dangle': ['error', {
    arrays: 'always-multiline', objects: 'always-multiline', imports: 'always-multiline', exports: 'always-multiline', functions: 'only-multiline',
  }],
  'function-paren-newline': ['error', 'multiline-arguments'],
  'linebreak-style': ['error', 'unix'],
  'no-trailing-spaces': 'error',
  'no-nested-ternary': 'error',
  'padding-line-between-statements': ['error',
    { blankLine: 'always', prev: '*', next: ['multiline-block-like', 'multiline-const', 'multiline-let', 'multiline-expression'] },
    { blankLine: 'always', prev: ['multiline-block-like', 'multiline-const', 'multiline-let', 'multiline-expression'], next: '*' },
    // Stacked case labels share one body; a blank line between them trips `no-fallthrough`.
    { blankLine: 'any', prev: 'case', next: ['case', 'default'] },
  ],
  // TypeScript resolves identifiers itself.
  'no-undef': 'off',
  '@typescript-eslint/no-explicit-any': 'off',
  '@typescript-eslint/no-unused-vars': 'off',
};

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    ignores: [
      'dist/',
      'coverage/',
      'node_modules/',
      '*.tsbuildinfo',
    ],
  },
  {
    rules: sharedRules,
  },
);
