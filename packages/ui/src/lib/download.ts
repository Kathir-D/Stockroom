/**
 * Hand a Blob to the browser as a file download.
 *
 * The label sheets and ID cards are POST responses behind a bearer token, so
 * they cannot be a plain link. The object URL is revoked on the next tick
 * rather than immediately: Safari starts the download asynchronously and a
 * URL revoked in the same task downloads nothing.
 */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
