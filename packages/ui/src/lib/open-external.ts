/**
 * Opening a page outside the app — Google's sign-in, for the one-click
 * Google connection.
 *
 * Two hosts, two ways. The Wails window cannot open a tab of its own, but
 * injects `window.runtime.BrowserOpenURL`, which hands the link to the system
 * browser. A browser tab can open another — but only during the click: a
 * popup opened after an `await` is blocked. The link comes back from the
 * server, so the tab is opened blank *during* the click and pointed at the
 * link once it arrives.
 */

type WailsRuntime = { BrowserOpenURL?: (url: string) => void };

function wails(): WailsRuntime | undefined {
  return (globalThis as { runtime?: WailsRuntime }).runtime;
}

export interface PendingWindow {
  /** Points the opened window at `url`; false if nothing could be opened. */
  go(url: string): boolean;
  /** Closes a blank window that is no longer needed. */
  cancel(): void;
}

/** Call synchronously inside the click handler, before any `await`. */
export function beginExternal(): PendingWindow {
  const runtime = wails();
  if (runtime?.BrowserOpenURL) {
    return {
      go(url) {
        runtime.BrowserOpenURL!(url);
        return true;
      },
      cancel() {},
    };
  }
  const win = typeof window === "undefined" ? null : window.open("about:blank", "_blank");
  return {
    go(url) {
      if (!win || win.closed) return false;
      // The page is Google's; it has no business reaching back into this one.
      win.opener = null;
      win.location.href = url;
      return true;
    },
    cancel() {
      if (win && !win.closed) win.close();
    },
  };
}
