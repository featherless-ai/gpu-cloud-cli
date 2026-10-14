export function describeError(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }

  if (err && typeof err === 'object') {
    const record = err as Record<string, unknown>;
    const nested = record.message ?? record.body ?? record.error;

    if (typeof nested === 'string' && nested.length > 0) {
      return nested;
    }

    try {
      const json = JSON.stringify(err);

      if (json && json !== '{}') {
        return json;
      }
    } catch {
      /* fall through */
    }
  }

  return String(err);
}

export class CliError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CliError';
  }
}

export class UnauthorizedError extends CliError {
  constructor(message = 'Unauthorized') {
    super(message);
    this.name = 'UnauthorizedError';
  }
}

/** Authenticated, but the key lacks a required scope. */
export class ForbiddenError extends CliError {
  constructor(message = 'Forbidden') {
    super(message);
    this.name = 'ForbiddenError';
  }
}

export class RetryableError extends CliError {
  constructor(message: string) {
    super(message);
    this.name = 'RetryableError';
  }
}

/** Resource missing or outside the caller's organization. */
export class NotFoundError extends CliError {
  constructor(message = 'Not found') {
    super(message);
    this.name = 'NotFoundError';
  }
}

/** Server rejected the request state (409). `code` is the machine code. */
export class ConflictError extends CliError {
  readonly code: string;

  constructor(code: string, message = 'Conflict') {
    super(message);
    this.name = 'ConflictError';
    this.code = code;
  }
}

export class RateLimitError extends RetryableError {
  constructor(message = 'Rate limited') {
    super(message);
    this.name = 'RateLimitError';
  }
}

export class ServiceUnavailableError extends RetryableError {
  constructor(message = 'Service unavailable') {
    super(message);
    this.name = 'ServiceUnavailableError';
  }
}
