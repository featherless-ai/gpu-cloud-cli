import { Argument, Command, Option } from 'commander';
import { SCHEME } from './commands/volume/files';
import type { CloudClient } from './clients/cloud';
import { APP_NAME } from './version';

export interface CompleterContext {
  /** The --profile value on the line being completed. */
  profile?: string;
  /** Positional values typed so far, e.g. the job id for `job logs <id> --attempt`. */
  args: string[];
  /** Client authenticated for `profile`, created once per Tab. */
  cloud: () => Promise<CloudClient>;
}

/** Candidates for the word being typed; empty lets the shell complete local files. */
export type Completer = (word: string, ctx: CompleterContext) => string[] | Promise<string[]>;

/** Third argument of CliCommand option()/argument(); Commander's positional forms still work. */
export interface ValueConfig<T = unknown> {
  defaultValue?: T;
  parse?: (value: string, previous: T) => T;
  choices?: readonly string[];
  complete?: Completer;
}

export interface CompletionOption {
  flags: string;
  required: boolean;
  complete?: Completer;
}

export interface CompletionArgument {
  variadic: boolean;
  complete?: Completer;
}

export interface CompletionNode {
  name: string;
  options: CompletionOption[];
  arguments: CompletionArgument[];
  commands: CompletionNode[];
  /** Set on the help node: the command whose subcommands `help` lists. */
  helpFor?: CompletionNode;
}

export interface CompletionPosition {
  node: CompletionNode;
  /** The word before this one is an option awaiting its value. */
  afterFlag: CompletionOption | null;
  /** Positional values before the word being completed. */
  args: string[];
}

const completers = new WeakMap<Argument | Option, Completer>();

function isConfig(value: unknown): value is ValueConfig {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && !(value instanceof RegExp);
}

/** Commander command whose options and arguments can declare a completer: `.option(flags, description, { complete })`. */
export class CliCommand extends Command {
  override createCommand(name?: string): CliCommand {
    return new CliCommand(name);
  }

  override option<T>(flags: string, description: string, config: ValueConfig<T>): this;
  override option(flags: string, description?: string, defaultValue?: string | boolean | string[]): this;
  override option<T>(flags: string, description: string, parseArg: (value: string, previous: T) => T, defaultValue?: T): this;
  override option(flags: string, description: string, regexp: RegExp, defaultValue?: string | boolean | string[]): this;
  override option(flags: string, description?: string, ...rest: unknown[]): this {
    const [config] = rest;

    if (!isConfig(config)) {
      return Reflect.apply(super.option, this, [flags, description, ...rest]) as this;
    }

    const option = this.createOption(flags, description);

    if (config.defaultValue !== undefined) {
      option.default(config.defaultValue);
    }

    if (config.choices) {
      option.choices(config.choices);
    }

    if (config.parse) {
      option.argParser(config.parse);
    }

    if (config.complete) {
      completers.set(option, config.complete);
    }

    return this.addOption(option);
  }

  override argument<T>(name: string, description: string, config: ValueConfig<T>): this;
  override argument<T>(flags: string, description: string, parseArg: (value: string, previous: T) => T, defaultValue?: T): this;
  override argument(name: string, description?: string, defaultValue?: unknown): this;
  override argument(name: string, description?: string, ...rest: unknown[]): this {
    const [config] = rest;

    if (!isConfig(config)) {
      return Reflect.apply(super.argument, this, [name, description, ...rest]) as this;
    }

    const argument = this.createArgument(name, description);

    if (config.defaultValue !== undefined) {
      argument.default(config.defaultValue);
    }

    if (config.choices) {
      argument.choices(config.choices);
    }

    if (config.parse) {
      argument.argParser(config.parse);
    }

    if (config.complete) {
      completers.set(argument, config.complete);
    }

    return this.addArgument(argument);
  }
}

/** A declared completer, or the Commander choices as one. */
function completerOf(target: Argument | Option): Completer | undefined {
  const choices = target.argChoices;

  return completers.get(target) ?? (choices && (word => choices.filter(choice => choice.startsWith(word))));
}

/** The command tree the generated scripts complete; hidden commands excluded, global options and help ride along. */
export function commandTree(program: Command, inherited: CompletionOption[] = []): CompletionNode {
  const options = dedupeOptions([
    ...inherited,
    ...program.options.map(option => ({ flags: option.flags, required: option.required, complete: completerOf(option) })),
    { flags: '--help', required: false },
  ]);

  const node: CompletionNode = {
    name: program.name() || APP_NAME,
    options,
    arguments: program.registeredArguments.map(argument => ({ variadic: argument.variadic, complete: completerOf(argument) })),
    commands: [],
  };

  for (const command of program.commands) {
    if ('_hidden' in command && command._hidden === true) {
      continue;
    }

    const child = commandTree(command, options);

    node.commands.push(child);

    for (const alias of command.aliases()) {
      node.commands.push({ ...child, name: alias });
    }
  }

  if (node.commands.length) {
    node.commands.push({
      name: 'help', options, arguments: [], commands: [], helpFor: node,
    });
  }

  return node;
}

function dedupeOptions(options: CompletionOption[]): CompletionOption[] {
  const seen = new Set<string>();

  return options.filter(option => !seen.has(option.flags) && (seen.add(option.flags), true));
}

/** Command-line words with escapes and quotes decoded. `open` = the last word is in progress. */
export function tokenizeLine(line: string): { words: string[]; open: boolean } {
  const words: string[] = [];
  let word = '';
  let started = false;
  let quote: string | null = null;
  let escaped = false;

  for (const c of line) {
    if (escaped) {
      word += c;
      started = true;
      escaped = false;
    } else if (c === '\\') {
      escaped = true;
    } else if (quote) {
      if (c === quote) {
        quote = null;
      } else {
        word += c;
      }
    } else if (c === '"' || c === '\'') {
      quote = c;
      started = true;
    } else if (c === ' ' || c === '\t') {
      if (started) {
        words.push(word);
        word = '';
        started = false;
      }
    } else {
      word += c;
      started = true;
    }
  }

  return { words: started ? [...words, word] : words, open: started };
}

/** Resolve a command line to its position: deepest command, pending option value, positional values. Expects the words after the program name. */
export function walkCommandTree(tree: CompletionNode, words: string[]): CompletionPosition {
  let node = tree;
  let afterFlag: CompletionOption | null = null;
  let literal = false;
  let args: string[] = [];

  for (const word of words) {
    if (afterFlag) {
      afterFlag = null;
      continue;
    }

    if (literal) {
      args.push(word);
      continue;
    }

    const child = node.commands.find(command => command.name === word);

    if (child) {
      node = child;
      args = [];
      continue;
    }

    if (word === '--') {
      literal = true;
      continue;
    }

    if (word.startsWith('--')) {
      if (!word.includes('=')) {
        afterFlag = node.options.find(option => option.required && longFlag(option.flags) === word) ?? null;
      }

      continue;
    }

    if (word.startsWith('-') && word.length > 1) {
      afterFlag = node.options.find(option => option.required && shortFlags(option.flags).includes(word)) ?? null;
      continue;
    }

    args.push(word);
  }

  return { node, afterFlag, args };
}

/** Candidates for the word being typed from the completers the command declares; empty lets the shell complete local files. */
export async function completeWord(current: string, position: CompletionPosition, ctx: CompleterContext): Promise<string[]> {
  const { node, afterFlag, args } = position;

  if (afterFlag) {
    return afterFlag.complete ? afterFlag.complete(current, ctx) : [];
  }

  const assignment = /^(--[^=]+)=(.*)$/s.exec(current);

  if (assignment) {
    const [, flag = '', value = ''] = assignment;
    const option = node.options.find(candidate => longFlag(candidate.flags) === flag);

    return option?.complete ? (await option.complete(value, ctx)).map(candidate => `${flag}=${candidate}`) : [];
  }

  if (current.startsWith('-')) {
    return [...new Set(node.options.flatMap(optionFlags))].filter(flag => flag.startsWith(current));
  }

  const target = node.helpFor ?? node;

  if (args.length === 0) {
    const commands = target.commands.filter(command => command.name.startsWith(current)).map(command => command.name);

    if (commands.length) {
      return commands;
    }
  }

  const last = node.arguments.at(-1);
  const argument = node.arguments[args.length] ?? (last?.variadic ? last : undefined);

  return argument?.complete ? argument.complete(current, ctx) : [];
}

function longFlag(flags: string): string {
  return flags.split(',').map(part => part.trim()).find(part => part.startsWith('--'))?.split(/[ =]/)[0] ?? '';
}

function shortFlags(flags: string): string[] {
  return flags
    .split(',')
    .map(part => part.trim())
    .filter(part => part.startsWith('-') && !part.startsWith('--'))
    .map(part => part.split(' ')[0]!)
    .filter(flag => flag.length === 2);
}

function optionFlags(option: CompletionOption): string[] {
  return [...shortFlags(option.flags), longFlag(option.flags)].filter(Boolean);
}

/** The --profile value on the raw command line, as the shell scripts pass COMP_LINE. */
export function profileFromCommand(line: string): string | undefined {
  const words = tokenizeLine(line).words;

  for (let index = words.length - 1; index >= 1; index--) {
    const word = words[index]!;

    if (word === '--profile') {
      return words[index + 1];
    }

    if (word.startsWith('--profile=')) {
      return word.slice('--profile='.length);
    }
  }

  return undefined;
}

export function bashScript(): string {
  const fn = `_${APP_NAME.replace(/-/g, '_')}_completions`;

  // Readline exports COMP_LINE/COMP_POINT to the function's environment; the
  // CLI tokenizes the whole line for command/flag context.
  //
  // Readline replaces only the part of the word after its last unquoted
  // COMP_WORDBREAKS `:` or `=` (`vol:` + `//data/…`, `--profile=` + `prod`),
  // or after an open quote. The scan finds that point as `brk`, and the text
  // before it comes off every candidate. Filename quoting would insert the
  // bare scheme as `vol\://`.
  //
  // Readline lays matches out in columns; padding each past half the screen
  // lists one per row. Paths keep columns: under -o filenames readline shows
  // only the text after their last `/`, which padding would blank.
  return `${fn}() {
  local line cur prefix c i q qpos esc2 start brk fc breaks pad
  breaks=\${COMP_WORDBREAKS//[^:=]/}
  line=\${COMP_LINE:0:$COMP_POINT}
  i=0 q= esc2=0 start=-1 brk=-1
  while [ $i -lt \${#line} ]; do
    c=\${line:$i:1}
    if [ $esc2 -eq 1 ]; then esc2=0
    elif [ "$c" = '\\' ]; then esc2=1
    elif [ -n "$q" ] && [ "$c" = "$q" ]; then q=
    elif [ -z "$q" ]; then
      case $c in
        \\"|\\') q=$c qpos=$i ;;
        ' '|$'\\t') start=$i brk=$i ;;
        *) [ -n "$breaks" ] && [[ $breaks == *"$c"* ]] && brk=$i ;;
      esac
    fi
    i=$((i+1))
  done
  [ -n "$q" ] && brk=$qpos
  cur=\${line:$((start + 1))}
  prefix=\${line:$((start + 1)):$((brk - start))}
  cur=\${cur//\\\\} prefix=\${prefix//\\\\}
  cur=\${cur//\\"/} prefix=\${prefix//\\"/}
  cur=\${cur//\\'/} prefix=\${prefix//\\'/}
  COMPREPLY=()
  while IFS= read -r fc; do
    COMPREPLY+=("$fc")
  done < <(COMP_LINE="$COMP_LINE" COMP_POINT="$COMP_POINT" FC_WORD="$cur" ${APP_NAME} _complete 2>/dev/null)
  [ -n "$prefix" ] && COMPREPLY=("\${COMPREPLY[@]#"$prefix"}")
  [ "\${COMPREPLY[*]}" = '${SCHEME}' ] && compopt +o filenames 2>/dev/null
  if [ \${#COMPREPLY[@]} -gt 1 ] && [[ \${COMPREPLY[*]} != */* ]]; then
    pad=$(printf "%$(( \${COLUMNS:-$(tput cols 2>/dev/null || echo 80)} / 2 ))s" '')
    for i in "\${!COMPREPLY[@]}"; do
      COMPREPLY[i]=\${COMPREPLY[i]}\${pad:\${#COMPREPLY[i]}}
    done
  fi
  if [ \${#COMPREPLY[@]} -gt 0 ]; then
    for fc in "\${COMPREPLY[@]}"; do
      case $fc in */|*:) ;; *) return 0 ;; esac
    done

    compopt -o nospace 2>/dev/null
  fi
}
complete -o default -o filenames -F ${fn} ${APP_NAME}
`;
}

export function zshScript(): string {
  const fn = `_${APP_NAME}`;

  // compinit autoloads the installed file (~/.zsh/completions/_featherless-cloud)
  // as the function body, so the first call defines the function and must
  // also run it; `eval "$(... completion zsh)"` registers it with compdef.
  // The words before the cursor and ${(Q)PREFIX}, the in-progress word
  // unquoted, go through the environment. compadd quotes the full-path
  // candidates on insertion; -d lists only their last path component, and
  // -l lists one per row.
  return `#compdef ${APP_NAME}
${fn}() {
  local -a cand nospace nospaceshown plain plainshown
  local c shown ret=1
  cand=( \${(f)"$(COMP_LINE="\${words[1,CURRENT-1]} " FC_WORD=\${(Q)PREFIX} ${APP_NAME} _complete 2>/dev/null)"} )
  if (( ! \${#cand} )); then
    _files
    return
  fi
  for c in "\${cand[@]}"; do
    shown=$c
    if [[ $c == vol://?*/* ]]; then
      shown=\${\${c%/}##*/}
      [[ $c == */ ]] && shown+=/
    fi
    if [[ $c == */ || $c == *: ]]; then
      nospace+=("$c") nospaceshown+=("$shown")
    else
      plain+=("$c") plainshown+=("$shown")
    fi
  done
  (( \${#nospace} )) && compadd -l -S '' -d nospaceshown -- "\${nospace[@]}" && ret=0
  (( \${#plain} )) && compadd -l -d plainshown -- "\${plain[@]}" && ret=0
  return ret
}
if [[ \${funcstack[1]} == ${fn} ]]; then
  ${fn} "$@"
else
  compdef ${fn} ${APP_NAME}
fi
`;
}
