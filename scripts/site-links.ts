import { posix, resolve } from "node:path";
import { decodeHTMLAttribute } from "entities";

/** Meta tags whose content is a URL on this site. */
const SHARED_META = new Set(["og:image", "og:url", "twitter:image"]);

/** Check rendered navigation, assets and anchors offline, including theme output. */
export async function checkSiteLinks(
  root: string,
  origin = "https://local-agent.dev",
) {
  const base = new URL(origin);
  if (!["https:", "http:"].includes(base.protocol))
    throw new Error("Invalid site origin");
  const files = new Set<string>();
  const pages = new Map<
    string,
    { anchors: Set<string>; references: string[] }
  >();
  for await (const file of new Bun.Glob("**/*").scan({
    cwd: root,
    dot: true,
    onlyFiles: true,
  })) {
    files.add(file);
    if (!file.endsWith(".html")) continue;
    const page = { anchors: new Set<string>(), references: [] as string[] };
    await new HTMLRewriter()
      .on("*", {
        element(element) {
          for (const attribute of ["id", "name"]) {
            const value = element.getAttribute(attribute);
            if (value !== null) page.anchors.add(decodeHTMLAttribute(value));
          }
          for (const attribute of ["href", "src"]) {
            const value = element.getAttribute(attribute);
            if (value) page.references.push(decodeHTMLAttribute(value));
          }
          // A link preview fetches its image and canonical page from these.
          if (element.tagName === "meta") {
            const key =
              element.getAttribute("property") ?? element.getAttribute("name");
            const content = element.getAttribute("content");
            if (content && key && SHARED_META.has(key))
              page.references.push(decodeHTMLAttribute(content));
          }
        },
      })
      .transform(new Response(Bun.file(resolve(root, file))))
      .text();
    pages.set(file, page);
  }
  if (!pages.size) throw new Error("No rendered HTML pages found");
  const failures: string[] = [];
  for (const [source, page] of pages) {
    for (const reference of page.references) {
      try {
        const target = new URL(reference, new URL(`/${source}`, base));
        if (target.origin !== base.origin) continue;
        const decoded = decodeURIComponent(target.pathname);
        if (decoded.includes("\0")) throw new Error("invalid path");
        const path = posix.normalize(`/${decoded}`).slice(1);
        const candidates = !path
          ? ["index.html"]
          : decoded.endsWith("/")
            ? [`${path.replace(/\/$/, "")}/index.html`]
            : path.endsWith(".html")
              ? [path]
              : [path, `${path}.html`, `${path}/index.html`];
        const file = candidates.find((candidate) => files.has(candidate));
        if (!file) throw new Error(`missing target (${candidates.join(", ")})`);
        const fragment = decodeURIComponent(target.hash.slice(1)).split(
          ":~:text=",
        )[0];
        if (
          fragment &&
          pages.has(file) &&
          !pages.get(file)?.anchors.has(fragment)
        )
          throw new Error(`missing fragment #${fragment} in ${file}`);
      } catch (error) {
        failures.push(
          `${source}: ${JSON.stringify(reference)} — ${error instanceof Error ? error.message : "invalid reference"}`,
        );
      }
    }
  }
  return failures.sort();
}

if (import.meta.main) {
  const failures = await checkSiteLinks(
    resolve(Bun.argv[2] ?? "docs/.vitepress/dist"),
  );
  if (failures.length) {
    console.error(failures.join("\n"));
    process.exitCode = 1;
  } else console.log("Rendered internal links and anchors passed");
}
