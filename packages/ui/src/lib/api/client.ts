/**
 * The one HTTP client for the Stockroom API.
 *
 * Both frontends call the Go server and nothing else: no supabase-js, no
 * PostgREST, no database credentials in TypeScript (CLAUDE.md §4). Every
 * endpoint wrapper in `./index.ts` goes through `request` below, so session
 * handling, error mapping and the idle-timeout hook exist exactly once.
 */

/** Errors the server names in its JSON body, mapped from Go sentinels. */
export class ApiError extends Error {
  readonly status: number;
  /** Set on a 403 from a limited session: the UI must go to set-password. */
  readonly needsPassword: boolean;
  /** The parsed body, for the handful of callers that read more than `error`. */
  readonly body: Record<string, unknown>;

  constructor(
    status: number,
    message: string,
    body: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.needsPassword = body.needs_password === true;
    this.body = body;
  }

  /** 401. The session expired or was never valid; sign the user out. */
  get isUnauthorized() {
    return this.status === 401;
  }

  /**
   * 409. Either an item stopped being available mid-cart or the custodian is
   * overdue. `CheckOutAssets` is one transaction, so a 409 means *nothing*
   * changed and the cart is still intact (design-system.md §8.5).
   */
  get isConflict() {
    return this.status === 409;
  }

  /** 503 with its message intact: an unset UPLOADS_DIR or BACKUP_DIR in .env. */
  get isNotConfigured() {
    return this.status === 503;
  }
}

export interface ApiConfig {
  /** Origin of the Go server, no trailing slash. */
  baseUrl: string;
  /**
   * Called whenever a request comes back 401. The app uses it to clear the
   * session and the cart and return to sign-in: an idle timeout is the one
   * thing besides sign-out that empties the cart (design-system.md §8.4).
   */
  onUnauthorized?: () => void;
}

/**
 * Where the Go server lives. `CLAUDE.md` §9 pins it to 127.0.0.1:8080 on both
 * hosts; the Wails app and the Vite dev server are separate origins from it,
 * which is why the bearer token exists alongside the cookie the server also
 * sets (CLAUDE.md §7).
 */
export const DEFAULT_BASE_URL = "http://127.0.0.1:8080";

const config: ApiConfig = { baseUrl: DEFAULT_BASE_URL };

/** Each app calls this once at startup, from its own `src/lib/api.ts`. */
export function configureApi(next: Partial<ApiConfig>) {
  Object.assign(config, next);
}

export function apiBaseUrl() {
  return config.baseUrl;
}

/**
 * When a request last completed, in `performance.now()` terms.
 *
 * The server's idle timeout is measured from the last request it saw, so this
 * is the client's view of the same clock. `lib/keep-alive.ts` compares it
 * against the last user interaction to decide whether someone is being timed
 * out while actively using the app.
 */
let lastRequestMs = typeof performance === "undefined" ? 0 : performance.now();

export function lastRequestAt(): number {
  return lastRequestMs;
}

/**
 * The session token, held in memory and mirrored into sessionStorage.
 *
 * sessionStorage rather than localStorage: the closet PC is shared, so a
 * session must not outlive the browser tab it was created in. The server also
 * sets an HttpOnly cookie, but the Wails webview and the Vite dev server are
 * cross-origin to 127.0.0.1:8080, so the header is the path that actually
 * works on both hosts.
 */
const TOKEN_KEY = "stockroom_token";
let token: string | null = null;

function storage(): Storage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    // A webview with storage disabled still has to run; the in-memory copy is
    // enough for a single session.
    return null;
  }
}

export function getToken(): string | null {
  if (token === null) token = storage()?.getItem(TOKEN_KEY) ?? null;
  return token;
}

export function setToken(next: string | null) {
  token = next;
  const store = storage();
  if (!store) return;
  if (next === null) store.removeItem(TOKEN_KEY);
  else store.setItem(TOKEN_KEY, next);
}

/**
 * Turn a server-relative path into something an `<img>` can load.
 *
 * Two callers: a stored `photo_url` under `/files/`, and a sign-in photo wall
 * tile under `/signin-photos/`. Both are served by the Go server off disk, and
 * both frontends run on a different origin from it, so the base URL has to be
 * put back on. An absolute URL is passed through untouched.
 */
export function fileUrl(path: string | null | undefined): string | null {
  if (!path) return null;
  if (/^https?:\/\//.test(path)) return path;
  return config.baseUrl + path;
}

interface RequestOptions {
  method?: string;
  /** Serialised as JSON. Mutually exclusive with `form`. */
  body?: unknown;
  /** Sent as-is, for the multipart endpoints (asset photo, roster import). */
  form?: FormData;
  query?: Record<string, string | undefined>;
  /** Skip the token, for the two login routes and /health. */
  anonymous?: boolean;
  signal?: AbortSignal;
  /**
   * Return the body as a Blob instead of parsing JSON: the label sheets, the
   * ID cards and the barcode image. An error is still JSON and still throws
   * an ApiError with the server's message.
   */
  blob?: boolean;
}

async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const { method = "GET", body, form, query, anonymous, signal, blob } = options;

  let url = config.baseUrl + path;
  if (query) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== "") params.set(key, value);
    }
    const qs = params.toString();
    if (qs) url += "?" + qs;
  }

  const headers: Record<string, string> = {};
  if (!anonymous) {
    const tok = getToken();
    if (tok) headers.Authorization = `Bearer ${tok}`;
  }
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let response: Response;
  try {
    response = await fetch(url, {
      method,
      headers,
      body: form ?? (body === undefined ? undefined : JSON.stringify(body)),
      // The cookie the server sets is same-origin only, but sending it costs
      // nothing and makes a same-origin deployment work without the header.
      credentials: "include",
      signal,
    });
  } catch (cause) {
    // A dead server and a bad URL both land here. Name it plainly: the first
    // thing to check is whether `go run ./server` is running.
    throw new ApiError(
      0,
      `cannot reach the Stockroom server at ${config.baseUrl}`,
      {
        cause: String(cause),
      },
    );
  }

  // Stamped on any answer, including an error: the server refreshed the
  // session's deadline the moment it resolved the token, whatever it then
  // decided about the request.
  if (typeof performance !== "undefined") lastRequestMs = performance.now();

  if (blob && response.ok) return (await response.blob()) as T;

  const text = await response.text();
  let parsed: unknown = null;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = null;
    }
  }

  if (!response.ok) {
    const objBody =
      parsed && typeof parsed === "object"
        ? (parsed as Record<string, unknown>)
        : {};
    const message =
      typeof objBody.error === "string" && objBody.error
        ? objBody.error
        : `${response.status} ${response.statusText}`;
    const error = new ApiError(response.status, message, objBody);
    // A 401 is the idle timeout in almost every case, and the app has to react
    // to it wherever it happens rather than at each call site.
    if (error.isUnauthorized) config.onUnauthorized?.();
    throw error;
  }

  return parsed as T;
}

export { request };
