import { exec, spawn } from 'node:child_process';

/** Best-effort default-browser launch. The URL is also printed for manual paste. */
export function openBrowser(url: string): void {
  try {
    if (process.platform === 'win32') {
      // `start` is a cmd builtin; the URL is our own construction, so quoting it is safe.
      exec(`start "" "${url}"`);
      return;
    }

    const command = process.platform === 'darwin' ? 'open' : 'xdg-open';
    spawn(command, [url], { stdio: 'ignore' }).unref();
  } catch {
    /* non-fatal — the URL is printed either way */
  }
}
