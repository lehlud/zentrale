export const connectionErrorMessage = "Verbindung zum Server unterbrochen";

export class ConnectionError extends Error {
  constructor(cause: unknown) {
    super(connectionErrorMessage, { cause });
    this.name = "ConnectionError";
  }
}

/** One outage belongs to the application, even when several queries fail. */
export class ConnectionMonitor {
  private interrupted = false;
  private listeners = new Set<() => void>();

  private timer: ReturnType<typeof setTimeout> | undefined;
  private pending: Promise<void> | undefined;
  private controller: AbortController | undefined;
  private delay = 2000;
  private active = true;

  constructor(
    private check: (signal: AbortSignal) => Promise<unknown>,
    private onRestored: () => void,
  ) {}

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = () => this.interrupted;

  reportFailure() {
    if (!this.interrupted) {
      this.interrupted = true;
      this.listeners.forEach((listener) => listener());
    }
    this.schedule();
  }

  resume() {
    this.active = true;
    this.schedule();
  }

  pause() {
    this.active = false;
    clearTimeout(this.timer);
    this.timer = undefined;
    this.controller?.abort();
  }

  retry = (): Promise<void> => {
    if (this.pending) {
      return this.pending;
    }

    if (!this.interrupted || !this.active) {
      return Promise.resolve();
    }

    clearTimeout(this.timer);
    this.timer = undefined;
    const controller = new AbortController();
    this.controller = controller;
    const timeout = setTimeout(() => controller.abort(), 10000);
    this.pending = (async () => {
      try {
        await this.check(controller.signal);
        if (controller.signal.aborted || !this.active) {
          return;
        }

        this.interrupted = false;
        this.delay = 2000;
        this.listeners.forEach((listener) => listener());
        this.onRestored();
      } catch {
        // Health probes never replay arbitrary mutations. The separate input
        // outbox may retry only its idempotent, receipt-addressed submissions.
        this.delay = Math.min(this.delay * 2, 30000);
      } finally {
        clearTimeout(timeout);
        this.controller = undefined;
        this.pending = undefined;
        this.schedule();
      }
    })();

    return this.pending;
  };

  private schedule() {
    if (!this.active || !this.interrupted || this.timer || this.pending) {
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.retry();
    }, this.delay);
  }
}
