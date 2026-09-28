/**
 * The photo dialog's checks. They run on every way a photo arrives, so a gap
 * here is an upload the server refuses after the wait, or one it stores under
 * the wrong type.
 */

import { describe, expect, it } from "vitest"
import { cameraErrorMessage, checkPhoto, MAX_PHOTO_BYTES } from "./photo-file"

function file(name: string, type: string, size = 10): File {
  return new File([new Uint8Array(size)], name, { type })
}

describe("checkPhoto", () => {
  it("renames a pasted image after its type", () => {
    const r = checkPhoto(file("image", "image/png"))
    expect(r.ok && r.file.name).toBe("photo.png")
  })

  it("names a JPEG .jpg whatever it was called", () => {
    const r = checkPhoto(file("IMG_0042.JPEG", "image/jpeg"))
    expect(r.ok && r.file.name).toBe("photo.jpg")
    expect(r.ok && r.file.type).toBe("image/jpeg")
  })

  it("falls back to the extension when there is no MIME type", () => {
    const r = checkPhoto(file("lens.webp", ""))
    expect(r.ok && r.file.name).toBe("photo.webp")
  })

  it("refuses a type the server refuses", () => {
    for (const f of [file("clip.mov", "video/quicktime"), file("logo.svg", "image/svg+xml"), file("scan.heic", "image/heic")]) {
      const r = checkPhoto(f)
      expect(r.ok).toBe(false)
    }
  })

  it("refuses a file with neither a type nor an extension", () => {
    expect(checkPhoto(file("photo", "")).ok).toBe(false)
  })

  it("refuses an empty file", () => {
    expect(checkPhoto(file("a.png", "image/png", 0)).ok).toBe(false)
  })

  it("takes a file at the limit and refuses one past it", () => {
    expect(checkPhoto(file("a.jpg", "image/jpeg", MAX_PHOTO_BYTES)).ok).toBe(true)
    const r = checkPhoto(file("a.jpg", "image/jpeg", MAX_PHOTO_BYTES + 1))
    expect(r.ok).toBe(false)
    expect(!r.ok && r.message).toContain("10 MB")
  })
})

describe("cameraErrorMessage", () => {
  it("says a blocked camera is a site setting", () => {
    expect(cameraErrorMessage(new DOMException("denied", "NotAllowedError"))).toContain("site settings")
  })

  it("says a busy camera is held by another program", () => {
    expect(cameraErrorMessage(new DOMException("busy", "NotReadableError"))).toContain("another program")
  })

  it("says when there is no camera", () => {
    expect(cameraErrorMessage(new DOMException("none", "NotFoundError"))).toContain("No camera")
  })

  it("has an answer for anything else", () => {
    expect(cameraErrorMessage("boom")).toContain("Choose a file")
  })
})
