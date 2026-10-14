import type { Command } from 'commander';
import * as p from '@clack/prompts';

type OutputMode = 'json' | 'plain' | 'interactive';

interface TableOutput {
  table: { header: string[]; rows: string[][] };
  footer?: string;
}

interface TextOutput {
  text: string;
}

export type FormatterResult = TableOutput | TextOutput;

function formatTable(header: string[], rows: string[][]): string {
  const colWidths = header.map((h, i) =>
    Math.max(h.length, ...rows.map((r) => (r[i] ?? '').length)));

  const formatRow = (row: string[]) =>
    row.map((cell, i) => (cell ?? '').padEnd(colWidths[i] ?? 0)).join('  ');

  return [
    formatRow(header),
    colWidths.map((w) => '-'.repeat(w)).join('  '),
    ...rows.map(formatRow),
  ].join('\n');
}

function isTableOutput(result: FormatterResult): result is TableOutput {
  return 'table' in result;
}

export class CliContext {
  readonly mode: OutputMode;

  constructor(cmd: Command) {
    let root = cmd;

    while (root.parent) {
      root = root.parent;
    }

    const opts = root.opts();

    if (opts.json) {
      this.mode = 'json';
    } else if (opts.nonInteractive) {
      this.mode = 'plain';
    } else {
      this.mode = 'interactive';
    }
  }

  output<T>(data: T, formatter: (data: T) => FormatterResult): void {
    if (this.mode === 'json') {
      console.log(JSON.stringify(data, null, 2));
      return;
    }

    const result = formatter(data);

    if (isTableOutput(result)) {
      const text = formatTable(result.table.header, result.table.rows);

      if (this.mode === 'interactive') {
        p.log.info(text);

        if (result.footer) {
          p.log.info(result.footer);
        }
      } else {
        console.log(text);

        if (result.footer) {
          console.log(result.footer);
        }
      }
    } else {
      this.log(result.text);
    }
  }

  async spin<T>(label: string, fn: () => Promise<T>): Promise<T> {
    if (this.mode !== 'interactive') {
      return fn();
    }

    const s = p.spinner();
    s.start(label);

    try {
      const result = await fn();
      s.stop(`${label} done.`);
      return result;
    } catch (err) {
      s.stop('Failed.');
      throw err;
    }
  }

  log(message: string): void {
    if (this.mode === 'json') {
      return;
    }

    if (this.mode === 'interactive') {
      p.log.info(message);
    } else {
      console.log(message);
    }
  }

  success(message: string): void {
    if (this.mode === 'json') {
      return;
    }

    if (this.mode === 'interactive') {
      p.log.success(message);
    } else {
      console.log(message);
    }
  }

  warn(message: string): void {
    if (this.mode === 'json') {
      return;
    }

    if (this.mode === 'interactive') {
      p.log.warn(message);
    } else {
      console.error(`Warning: ${message}`);
    }
  }

  error(message: string): void {
    if (this.mode === 'json') {
      console.log(JSON.stringify({ error: message }));
    } else {
      console.error(`Error: ${message}`);
    }
  }

  fatal(message: string): never {
    this.error(message);
    process.exit(1);
  }
}

export function createContext(cmd: Command): CliContext {
  return new CliContext(cmd);
}
