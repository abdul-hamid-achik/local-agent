/** Minimal safe Markdown renderer: escape first, then decorate. */

function escapeHtml(text: string): string {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function inline(text: string): string {
  return escapeHtml(text)
    .replaceAll(/`([^`\n]+)`/g, "<code>$1</code>")
    .replaceAll(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replaceAll(/(^|\W)_([^_]+)_(\W|$)/g, "$1<em>$2</em>$3");
}

export function renderMarkdown(source: string): string {
  const lines = source.replaceAll("\r\n", "\n").split("\n");
  const out: string[] = [];
  let inCode = false;
  let codeFence = "";
  let codeLang = "";
  let codeBuf: string[] = [];
  let listOpen = false;

  const closeList = () => {
    if (listOpen) {
      out.push("</ul>");
      listOpen = false;
    }
  };
  const closeCode = () => {
    if (inCode) {
      out.push(
        `<pre><code${codeLang ? ` data-lang="${escapeHtml(codeLang)}"` : ""}>${escapeHtml(codeBuf.join("\n"))}</code></pre>`,
      );
      inCode = false;
      codeBuf = [];
    }
  };

  for (const line of lines) {
    // As CommonMark has it: three or more backticks or tildes open a block,
    // and the first word after them names its language. Only a bare fence
    // of the same kind and at least as long closes it, so a longer fence
    // holds a shorter one; an open block runs to the end.
    const fence = line.match(/^ {0,3}(`{3,}|~{3,})(.*)$/);
    const marks = fence?.[1] ?? "";
    const info = fence?.[2] ?? "";
    if (inCode) {
      if (
        marks[0] === codeFence[0] &&
        marks.length >= codeFence.length &&
        !info.trim()
      )
        closeCode();
      else codeBuf.push(line);
      continue;
    }
    // Backticks after a backtick fence make the line inline code instead.
    if (fence && !(marks[0] === "`" && info.includes("`"))) {
      closeList();
      inCode = true;
      codeFence = marks;
      codeLang = info.trim().split(/\s+/)[0] ?? "";
      continue;
    }
    const heading = line.match(/^(#{1,4})\s+(.+)$/);
    if (heading) {
      closeList();
      const level = heading[1]?.length ?? 1;
      out.push(`<h${level}>${inline(heading[2] ?? "")}</h${level}>`);
      continue;
    }
    const item = line.match(/^[-*]\s+(.+)$/);
    if (item) {
      if (!listOpen) {
        out.push("<ul>");
        listOpen = true;
      }
      out.push(`<li>${inline(item[1] ?? "")}</li>`);
      continue;
    }
    if (!line.trim()) {
      closeList();
      continue;
    }
    closeList();
    out.push(`<p>${inline(line)}</p>`);
  }
  closeCode();
  closeList();
  return out.join("");
}
