export type PatrisMethod =
  | 'app.get'
  | 'records.list'
  | 'info.get'
  | 'status.get'
  | 'config.get'
  | 'config.set'
  | 'toast.show'
  | 'refresh';

export interface PatrisRequest {
  requestId?: string | number | null;
  method: PatrisMethod;
  params?: unknown;
}

export interface PatrisError {
  code: string;
  message: string;
  retryable: boolean;
}

export interface PatrisMeta {
  requestId?: string | number | null;
  method?: PatrisMethod;
  mode?: string;
  origin?: string;
}

export type PatrisResult<T = unknown> =
  | { ok: true; result: T; meta: PatrisMeta }
  | { ok: false; error: PatrisError; meta: PatrisMeta };

export interface PatrisRendererClient {
  call<T = unknown>(method: PatrisMethod, params?: unknown): Promise<PatrisResult<T>>;
  status(): Promise<PatrisResult<PatrisHostStatus>>;
}

export interface PatrisAuthorizationContext {
  sourceUrl: string;
  action: 'invoke' | 'status';
  method: string | null;
}

export type PatrisPrivilegedAuthorizer = (
  event: unknown,
  context: Readonly<PatrisAuthorizationContext>
) => boolean | Promise<boolean>;

export interface PatrisHostStatus {
  lifecycle: 'idle' | 'initializing' | 'ready' | 'failed' | 'closing' | 'closed';
  ready: boolean;
  mode: string;
  attempts: Array<{ mode: string; code: string; message: string }>;
}

export interface PatrisTransport {
  name: string;
  initialize(): Promise<{ ready: boolean; [key: string]: unknown }>;
  call(method: PatrisMethod, params?: unknown): Promise<unknown>;
  close(): Promise<void> | void;
}

export const ALLOWED_METHODS: readonly PatrisMethod[];
export const HTTP_METHODS: Readonly<Record<PatrisMethod, readonly [string, string]>>;

export class PatrisHostError extends Error {
  constructor(code: string, message: string, options?: { retryable?: boolean; cause?: unknown });
  code: string;
  retryable: boolean;
  cause?: unknown;
}

export function normalizeOrigin(value: unknown): string;
export function compileOriginAllowlist(origins: string[]): Set<string>;
export function assertAllowedOrigin(sourceUrl: string, allowlist: ReadonlySet<string>): string;
export function assertAllowedMethod(method: unknown): PatrisMethod;
export function assertEnvelope<T = unknown>(value: unknown): PatrisResult<T>;
export function successEnvelope<T = unknown>(result: T, meta?: PatrisMeta): PatrisResult<T>;
export function errorEnvelope(error: unknown, meta?: PatrisMeta): PatrisResult<never>;

export class PrivilegedPatrisHost {
  constructor(options: { allowedOrigins: string[]; transports: PatrisTransport[] });
  initialize(): Promise<PatrisHostStatus>;
  status(): PatrisHostStatus;
  handle(sourceUrl: string, request: { requestId?: string | number; method: PatrisMethod; params?: unknown }): Promise<PatrisResult>;
  handleStatus(sourceUrl: string, requestId?: string | number | null): Promise<PatrisResult<PatrisHostStatus>>;
  close(): Promise<PatrisHostStatus>;
}

export function createConfiguredHost(options: {
  allowedOrigins: string[];
  dll?: { addonPath: string; dllPath: string; engineOptions?: Record<string, unknown>; timeoutMs?: number };
  executable?: { executablePath: string; databasePath: string; watch?: boolean; timeoutMs?: number };
  rest?: { baseUrl: string; timeoutMs?: number };
}): PrivilegedPatrisHost;

export function wrapExistingElectronBridge(bridge: {
  initialize(): Promise<{ ready: boolean; [key: string]: unknown }>;
  call(method: PatrisMethod, params?: unknown): Promise<unknown>;
  close?(): Promise<void> | void;
}): PatrisTransport;

export function createElectronRendererClient(options: {
  invoke(channel: string, request: unknown): Promise<unknown>;
  invokeChannel?: string;
  statusChannel?: string;
}): PatrisRendererClient;

export function sourceUrlFromElectronEvent(event: {
  senderFrame?: { url?: string } | null;
} | null | undefined): string;

export function registerElectronPatrisHost(options: {
  ipcMain: {
    handle(channel: string, handler: (event: unknown, request?: unknown) => Promise<PatrisResult>): void;
    removeHandler(channel: string): void;
  };
  host: PrivilegedPatrisHost;
  authorize: PatrisPrivilegedAuthorizer;
  invokeChannel?: string;
  statusChannel?: string;
}): () => void;

export function registerElectronShutdownBarrier(options: {
  app: {
    on(name: 'before-quit', listener: (event: { preventDefault(): void }) => void): void;
    removeListener(name: 'before-quit', listener: (event: { preventDefault(): void }) => void): void;
    quit(): void;
  };
  cleanup(): void | Promise<void>;
  onError?(error: unknown): void;
}): () => void;

export function createTauriRendererClient(options: {
  invoke(command: string, args: unknown): Promise<unknown>;
  invokeCommand?: string;
  statusCommand?: string;
}): PatrisRendererClient;

export function createTauriCommandHandlers(options: {
  host: PrivilegedPatrisHost;
  getSourceUrl(event: unknown): string | Promise<string>;
  authorize: PatrisPrivilegedAuthorizer;
}): {
  patrisInvoke(event: unknown, request: PatrisRequest): Promise<PatrisResult>;
  patrisStatus(event: unknown, request?: { requestId?: string | number | null }): Promise<PatrisResult<PatrisHostStatus>>;
};

export interface WebView2RendererBridge {
  postMessage(value: unknown): void;
  addEventListener(name: 'message', listener: (event: { data: unknown }) => void): void;
  removeEventListener?(name: 'message', listener: (event: { data: unknown }) => void): void;
}

export function createWebView2RendererClient(options: {
  webview: WebView2RendererBridge;
  timeoutMs?: number;
}): PatrisRendererClient & { dispose(): void };

export function createWebView2MessageHandler(options: {
  host: PrivilegedPatrisHost;
  getSourceUrl(event: unknown): string | Promise<string>;
  authorize: PatrisPrivilegedAuthorizer;
  postMessage(payload: unknown, event: unknown): void | Promise<void>;
}): (event: unknown) => Promise<boolean>;

export class RestTransport implements PatrisTransport {
  constructor(options: { baseUrl: string; name?: string; timeoutMs?: number; fetchImpl?: Function });
  name: string;
  initialize(): Promise<{ ready: boolean; [key: string]: unknown }>;
  call(method: PatrisMethod, params?: unknown): Promise<unknown>;
  close(): Promise<void>;
}

export class ExecutableTransport implements PatrisTransport {
  constructor(options: {
    executablePath: string;
    databasePath: string;
    watch?: boolean;
    timeoutMs?: number;
    startupAttempts?: number;
    startupDelayMs?: number;
    stopTimeoutMs?: number;
  });
  name: string;
  initialize(): Promise<{ ready: boolean; [key: string]: unknown }>;
  call(method: PatrisMethod, params?: unknown): Promise<unknown>;
  close(): Promise<void>;
}

export class NativeWorkerTransport implements PatrisTransport {
  constructor(options: {
    addonPath: string;
    dllPath: string;
    engineOptions?: Record<string, unknown>;
    workerPath?: string;
    timeoutMs?: number;
    stopTimeoutMs?: number;
  });
  name: string;
  initialize(): Promise<{ ready: boolean; [key: string]: unknown }>;
  call(method: PatrisMethod, params?: unknown): Promise<unknown>;
  close(): Promise<void>;
}
