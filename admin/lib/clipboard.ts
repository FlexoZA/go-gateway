// Copy text to the clipboard, working even when the page is served over plain
// HTTP. The async Clipboard API (navigator.clipboard) is only exposed in a
// secure context (HTTPS or localhost); on a staging box reached over http://
// it is undefined, so a naive writeText() silently does nothing. Fall back to a
// hidden <textarea> + execCommand("copy"), which still works there.
export async function copyText(text: string): Promise<boolean> {
  if (typeof navigator !== "undefined" && navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      /* fall through to the legacy path */
    }
  }
  if (typeof document === "undefined") return false;
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    // Keep it off-screen and unfocusable-looking but still selectable.
    ta.style.position = "fixed";
    ta.style.top = "-9999px";
    ta.setAttribute("readonly", "");
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
