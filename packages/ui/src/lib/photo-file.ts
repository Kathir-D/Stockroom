/**
 * The rules for an asset photo before it leaves the browser, shared by every
 * way one arrives in the photo dialog: a chosen file, a dropped file, a paste,
 * or a webcam snapshot.
 *
 * They mirror the server (internal/stockroom/photos.go and server/admin.go),
 * so a bad file is refused before an upload rather than after it. The server
 * still checks, and its answer is the one that counts.
 */

/** The types the server accepts, each with the extension it is saved under. */
const EXTENSIONS: Record<string, string> = {
  "image/jpeg": "jpg",
  "image/png": "png",
  "image/gif": "gif",
  "image/webp": "webp",
}

const TYPE_BY_EXTENSION: Record<string, string> = {
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  png: "image/png",
  gif: "image/gif",
  webp: "image/webp",
}

/**
 * The server caps the whole request at 10 MiB (maxPhotoBytes), and the
 * multipart wrapping counts toward it. Leaving 64 KiB for that keeps a file
 * that passes here from failing there.
 */
export const MAX_PHOTO_BYTES = 10 * 1024 * 1024 - 64 * 1024

/** What the dialog tells people the limits are. */
export const PHOTO_RULES = "JPEG, PNG, GIF or WebP, up to 10 MB"

export type PhotoCheck = { ok: true; file: File } | { ok: false; message: string }

/**
 * Checks a file and returns a copy named for its type.
 *
 * The server picks the saved type from the file name's extension, and a
 * pasted or dragged image often has no useful name ("image", or a browser's
 * own). Renaming it to `photo.<ext>` from its MIME type means what the server
 * stores matches what the bytes are.
 */
export function checkPhoto(file: File): PhotoCheck {
  let type = file.type.toLowerCase()
  if (!EXTENSIONS[type]) {
    // Some systems send no MIME type for a dragged file. Fall back to its
    // extension, which is what the server reads anyway.
    const ext = file.name.split(".").pop()?.toLowerCase() ?? ""
    type = file.name.includes(".") ? (TYPE_BY_EXTENSION[ext] ?? "") : ""
  }
  if (!type) {
    const what = file.name || file.type || "That file"
    return { ok: false, message: `${what} isn't a photo Stockroom can use. Use ${PHOTO_RULES}.` }
  }
  if (file.size === 0) {
    return { ok: false, message: "That file is empty." }
  }
  if (file.size > MAX_PHOTO_BYTES) {
    const mb = (file.size / (1024 * 1024)).toFixed(1)
    return {
      ok: false,
      message: `That photo is ${mb} MB. The limit is 10 MB, so take a smaller one or shrink it first.`,
    }
  }
  return { ok: true, file: new File([file], `photo.${EXTENSIONS[type]}`, { type }) }
}

/**
 * Turns a getUserMedia failure into a sentence that says what to do.
 *
 * The browser's own messages ("Permission denied", "Could not start video
 * source") don't say that the fix is a site setting, or another app holding
 * the camera.
 */
export function cameraErrorMessage(err: unknown): string {
  const name = typeof err === "object" && err !== null && "name" in err ? String(err.name) : ""
  switch (name) {
    case "NotAllowedError":
    case "PermissionDeniedError":
      return "The camera is blocked for this page. Allow camera access in the browser's site settings, then press Use the camera again."
    case "NotFoundError":
    case "DevicesNotFoundError":
    case "OverconstrainedError":
      return "No camera was found. Plug one in and try again, or choose a file instead."
    case "NotReadableError":
    case "TrackStartError":
      return "The camera is in use by another program, such as a video call or the closet camera. Close it and try again."
    case "SecurityError":
      return "This page isn't allowed to use a camera. Open Stockroom at http://localhost:8080 and try again."
    case "AbortError":
      return "The camera stopped before it started. Try again."
    default:
      return "The camera didn't start. Choose a file instead, or try again."
  }
}

/** Whether this browser can use a camera at all on this page. */
export function cameraSupported(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.mediaDevices?.getUserMedia === "function"
}
