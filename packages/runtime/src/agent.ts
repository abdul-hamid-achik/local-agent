import { createHash, randomUUID } from "node:crypto";
import type { Tool } from "ollama";
import { z } from "zod";
import { admitOllamaModel, admitOpenAILocalModel } from "#runtime/admission";
import {
  type ApprovedCommandProposal,
  createCommandProposal,
  executeApprovedCommand,
  parseArgv,
} from "#runtime/command";
import {
  CHARS_PER_TOKEN,
  COMPACT_HIGH,
  COMPACT_LOW,
  compactHistory,
  promptChars,
} from "#runtime/compact";
import type { Provider } from "#runtime/config";
import { postEditDiagnostics } from "#runtime/diagnostics";
import {
  chat,
  INVALID_ARGUMENTS,
  type Message,
  MessageSchema,
  type RunEvent,
  type ToolCall,
} from "#runtime/index";
import {
  ASK_USER_NOTE,
  hostInstructions,
  type Intent,
  projectInstructions,
} from "#runtime/instructions";
import { gatewayTarget, type McpConnection } from "#runtime/mcp";
import { assertFetchTarget, type FetchPolicy, fetchPolicy } from "#runtime/net";
import { sandboxUnavailable } from "#runtime/sandbox";
import type { ExecutionEvent, Session, SessionStore } from "#runtime/session";
import type {
  Workspace,
  WorkspaceEditProposal,
  WorkspaceWriteProposal,
} from "#runtime/workspace";

const ReadArgs = z.strictObject({ path: z.string().min(1).max(4096) });
// A line number or count; small models send "120" or null as often as 120.
const lineCount = z
  .union([z.number(), z.string().regex(/^\d+$/).transform(Number)])
  .nullish()
  .transform((value) => value || undefined)
  .pipe(z.number().int().min(1).max(10_000_000).optional());
const ReadRangeArgs = ReadArgs.extend({ offset: lineCount, limit: lineCount });
const EditArgs = ReadArgs.extend({
  old_string: z.string().min(1).max(100_000),
  new_string: z.string().max(100_000),
});
const WriteArgs = ReadArgs.extend({ content: z.string().max(1_000_000) });
// Small models send null or "" for an argument they leave out.
const optionalText = (max: number) =>
  z
    .string()
    .max(max)
    .nullish()
    .transform((value) => value || undefined);
const flag = z
  .union([
    z.boolean(),
    z.enum(["true", "false"]).transform((value) => value === "true"),
  ])
  .nullish();
// The read-only tools ignore keys they do not know (`recursive`, ...) instead of failing the call.
const ListArgs = z.object({ path: optionalText(4096) });
const SearchArgs = z.object({
  pattern: z.string().min(1).max(500),
  path: optionalText(4096),
  regex: flag,
  ignore_case: flag,
});
const FetchArgs = z.object({ url: z.string().min(1).max(8192) });
const WebSearchArgs = z.object({ query: z.string().min(1).max(500) });
const TaskArgs = z.strictObject({
  prompt: z.string().min(1).max(8000),
  // Small models send null or "" for an argument they leave out.
  max_turns: z
    .number()
    .int()
    .min(1)
    .max(16)
    .nullish()
    .transform((value) => value || 8),
});
const CommandArgs = z.strictObject({
  // An array, or (from a small model) a command line that is split without a shell.
  argv: z.union([
    z.array(z.string().max(100_000)).min(1).max(256),
    z.string().min(1).max(100_000),
  ]),
  timeout_ms: z.number().int().min(1).max(600_000).default(120_000),
});
const AskOption = z.object({
  id: z.string().trim().min(1).max(64),
  label: z.string().trim().min(1).max(256),
  detail: z.string().trim().max(512).nullish(),
});
// The kind follows the options when it is left out: a list is a choice.
// Only a choice keeps its options: yes/no and a typed answer have none.
const AskUserArgs = z
  .object({
    question: z.string().trim().min(1).max(2048),
    kind: z.enum(["yes_no", "single", "freetext"]).nullish(),
    options: z.array(AskOption).max(8).nullish(),
  })
  .transform(({ question, kind, options }) => {
    const resolved = kind ?? (options?.length ? "single" : "freetext");
    return {
      question,
      kind: resolved,
      options: resolved === "single" ? (options ?? []) : [],
    };
  })
  .refine(
    (args) => args.kind !== "single" || args.options.length >= 2,
    "A single choice needs at least two options",
  )
  .refine(
    (args) =>
      new Set(args.options.map((option) => option.id)).size ===
      args.options.length,
    "Option ids must be unique",
  );
const TodoItem = z.object({
  content: z.string().min(1).max(200),
  status: z.enum(["pending", "in_progress", "done"]),
});
// The list replaces the previous one entirely; at most one item is active.
const TodoWriteArgs = z.object({
  todos: z
    .array(TodoItem)
    .max(50)
    .refine(
      (todos) =>
        todos.filter((todo) => todo.status === "in_progress").length <= 1,
      "At most one todo may be in_progress; mark the previous one done or pending first",
    ),
});

const PATH_HELP =
  "relative to the workspace root, for example src/stats.ts (never /src/stats.ts)";
const pathProperty = (extra = "") => ({
  type: "string",
  description: `Path ${PATH_HELP}${extra}`,
});

const FETCH_TIMEOUT_MS = 15_000;
const FETCH_MAX_BYTES = 512 * 1024;
// Docs URLs redirect constantly (http→https, trailing-slash moves); following
// them is friendlier than the MCP policy of refusing redirects, but a manual
// loop keeps the hop count bounded.
const FETCH_MAX_REDIRECTS = 5;
const SEARXNG_TIMEOUT_MS = 10_000;
const BROWSER_UA =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36";
/** Every fetch request presents as a normal browser. The referer is added in
 * httpGet: it stays the origin of the requested URL (image servers with
 * hotlink protection check it, and the image branch cannot know the content
 * type before the download, so every request carries the same origin). */
const FETCH_HEADERS: Record<string, string> = {
  "user-agent": BROWSER_UA,
  accept: "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8",
};

/** GET with redirects followed, capped at FETCH_MAX_REDIRECTS hops. The
 * policy is checked before every request, so a redirect cannot reach a
 * scheme or address the first URL could not. */
async function httpGet(
  url: string,
  timeoutMs: number,
  policy: FetchPolicy,
  signal: AbortSignal,
  headers?: Record<string, string>,
): Promise<{ response: Response; finalUrl: string }> {
  let current = new URL(url);
  const origin = current.origin;
  const deadline = AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)]);
  for (let hop = 0; ; hop++) {
    await assertFetchTarget(current, policy, deadline);
    deadline.throwIfAborted();
    const response = await fetch(current, {
      redirect: "manual",
      headers: { ...headers, referer: origin },
      signal: deadline,
    });
    const location =
      response.status >= 300 && response.status < 400
        ? response.headers.get("location")
        : null;
    if (location === null) return { response, finalUrl: current.toString() };
    await response.body?.cancel();
    if (hop >= FETCH_MAX_REDIRECTS)
      throw new Error(`fetch: more than ${FETCH_MAX_REDIRECTS} redirects`);
    current = new URL(location, current);
  }
}

/** Reads at most maxBytes of the body as bytes (the image branch). */
async function readBytesCapped(
  response: Response,
  maxBytes: number,
): Promise<{ bytes: Buffer; truncated: boolean }> {
  const reader = response.body?.getReader();
  if (reader === undefined) return { bytes: Buffer.alloc(0), truncated: false };
  const chunks: Uint8Array[] = [];
  let kept = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (kept + value.length > maxBytes) {
      chunks.push(value.slice(0, maxBytes - kept));
      await reader.cancel();
      return { bytes: Buffer.concat(chunks), truncated: true };
    }
    chunks.push(value);
    kept += value.length;
  }
  return { bytes: Buffer.concat(chunks), truncated: false };
}

/** Reads at most maxBytes of the body as text. */
async function readCapped(
  response: Response,
  maxBytes: number,
): Promise<{ text: string; truncated: boolean }> {
  const reader = response.body?.getReader();
  if (reader === undefined) return { text: "", truncated: false };
  const chunks: Uint8Array[] = [];
  let kept = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (kept + value.length > maxBytes) {
      chunks.push(value.slice(0, maxBytes - kept));
      await reader.cancel();
      return {
        text: await new Blob(chunks as BlobPart[]).text(),
        truncated: true,
      };
    }
    chunks.push(value);
    kept += value.length;
  }
  return {
    text: await new Blob(chunks as BlobPart[]).text(),
    truncated: false,
  };
}

const HTML_BLOCK_TAGS: Record<string, true> = {
  address: true,
  article: true,
  aside: true,
  blockquote: true,
  br: true,
  dd: true,
  div: true,
  dl: true,
  dt: true,
  h1: true,
  h2: true,
  h3: true,
  h4: true,
  h5: true,
  h6: true,
  header: true,
  li: true,
  main: true,
  nav: true,
  ol: true,
  p: true,
  pre: true,
  section: true,
  table: true,
  tr: true,
  ul: true,
};

/** HTML to readable plain text: script/style dropped, blocks newline-separated. */
async function htmlToText(html: string): Promise<string> {
  const stripped = await new HTMLRewriter()
    .on("script,style,noscript,template,svg", {
      element: (el) => {
        el.remove();
      },
    })
    .transform(new Response(html))
    .text();
  const parts: string[] = [];
  await new HTMLRewriter()
    .on("*", {
      element: (el) => {
        if (HTML_BLOCK_TAGS[el.tagName]) parts.push("\n");
      },
    })
    .onDocument({
      text: (t) => {
        parts.push(t.text);
      },
    })
    .transform(new Response(stripped))
    .text();
  return parts
    .join("")
    .replace(/[ \t]+/g, " ")
    .replace(/\n\s*\n+/g, "\n\n")
    .trim();
}

type ExtractedLink = { url: string; text: string; score: number };
type ExtractedImage = {
  url: string;
  width?: number;
  height?: number;
  map: boolean;
  alt: string;
  score: number;
};

const EXTRACTED_FOOTER_MAX = 4000;
const EXTRACT_LINK_LIMIT = 8;
const EXTRACT_IMAGE_LIMIT = 3;
/** Stylesheets, scripts and icons: navigation chrome, not content. */
const NAV_ASSET = /\.(css|js|svg)([?#]|$)/i;
const MAP_WORDS = /map|atlas|region|world/i;

/** Resolves one attribute value to an absolute http(s) URL, or undefined for
 * anchors, mailto:, javascript:, data: and anything unparseable. */
function absoluteUrl(value: string | null, base: string): string | undefined {
  const trimmed = value?.trim();
  if (!trimmed || trimmed.startsWith("#")) return undefined;
  if (/^(mailto|javascript|data|tel):/i.test(trimmed)) return undefined;
  try {
    const url = new URL(trimmed, base);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    return url.toString();
  } catch {
    return undefined;
  }
}

/** Srcset candidates: "url 640w", "url 640x480" or a density "url 2x"
 * (density without a pixel width is unusable for scoring and is dropped). */
function srcsetCandidates(
  srcset: string | null,
): { url: string; width?: number; height?: number }[] {
  if (!srcset) return [];
  const out: { url: string; width?: number; height?: number }[] = [];
  for (const part of srcset.split(",")) {
    const [url, descriptor] = part.trim().split(/\s+/);
    if (!url) continue;
    if (!descriptor) {
      out.push({ url });
      continue;
    }
    const widthOnly = descriptor.match(/^(\d+)w$/);
    if (widthOnly) {
      out.push({ url, width: Number(widthOnly[1]) });
      continue;
    }
    const both = descriptor.match(/^(\d+)x(\d+)$/);
    if (both)
      out.push({ url, width: Number(both[1]), height: Number(both[2]) });
  }
  return out;
}

/** Paths that are navigation chrome on most sites: never worth a source. */
const NAV_PATHS = new Set([
  "/",
  "/shorts",
  "/login",
  "/signup",
  "/register",
  "/account",
  "/settings",
  "/search",
]);

/** Pages that answer with a bot challenge instead of content. The marker
 *  must be page-fatal: `challenge-platform` alone is injected into ordinary
 *  pages by Bot Fight Mode and would reject valid content. */
const BOT_CHALLENGE =
  /Just a moment\.\.\.|cf-browser-verification|Enable JavaScript and cookies to continue/i;

/** URL or alt words that mark decorative image chrome. */
const IMAGE_CHROME = /(logo|icon|sprite|avatar|favicon|emoji|badge)/i;

/** Below this score a link is chrome: short text, one path segment. */
const LINK_MIN_SCORE = 4;

/** Links are ranked, not first-seen: chrome pages lose to content. */
function linkScore(url: string, text: string, base: URL): number {
  const parsed = new URL(url);
  if (parsed.origin === base.origin && NAV_PATHS.has(parsed.pathname)) return 0;
  const segments = parsed.pathname.split("/").filter(Boolean);
  let score = 1 + Math.min(text.trim().length, 40) / 4;
  if (segments.length >= 2) score += 10;
  if (/\/(wiki|wikis|guide|guides|walkthrough)\b/.test(parsed.pathname))
    score += 20;
  if (segments.length === 1 && segments[0] === "") score = 0;
  return score;
}

/**
 * One extra HTMLRewriter pass over the same HTML htmlToText converts: the top
 * links (ranked by anchor text and path specificity, chrome skipped) and top
 * images (scored by area, map-ish alt text boosted), absolute URLs only,
 * deduped.
 */
async function extractHtmlAssets(
  html: string,
  baseUrl: string,
): Promise<{ links: ExtractedLink[]; images: ExtractedImage[] }> {
  const base = new URL(baseUrl);
  const links: ExtractedLink[] = [];
  const seenLinks = new Set<string>();
  let pendingLink: { url: string; text: string } | undefined;
  const images: ExtractedImage[] = [];
  const seenImages = new Set<string>();
  await new HTMLRewriter()
    .on("a", {
      element: (el) => {
        const url = absoluteUrl(el.getAttribute("href"), baseUrl);
        if (!url || seenLinks.has(url) || NAV_ASSET.test(url)) return;
        pendingLink = { url, text: "" };
        el.onEndTag(() => {
          if (pendingLink) {
            const score = linkScore(pendingLink.url, pendingLink.text, base);
            if (score >= LINK_MIN_SCORE) links.push({ ...pendingLink, score });
            seenLinks.add(pendingLink.url);
          }
          pendingLink = undefined;
        });
      },
      text: (chunk) => {
        if (pendingLink) pendingLink.text += chunk.text;
      },
    })
    .on("img", {
      element: (el) => {
        const widthAttr = Number(el.getAttribute("width"));
        const heightAttr = Number(el.getAttribute("height"));
        // A declared width under 16px is an icon, whatever its src says.
        if (Number.isFinite(widthAttr) && widthAttr > 0 && widthAttr < 16)
          return;
        const candidates = srcsetCandidates(el.getAttribute("srcset"));
        const fromSrc = absoluteUrl(el.getAttribute("src"), baseUrl);
        const url =
          (fromSrc && !NAV_ASSET.test(fromSrc) ? fromSrc : undefined) ??
          candidates
            .map((candidate) => absoluteUrl(candidate.url, baseUrl))
            .find((candidate) => candidate && !NAV_ASSET.test(candidate)) ??
          absoluteUrl(el.getAttribute("data-src"), baseUrl);
        if (!url || seenImages.has(url) || NAV_ASSET.test(url)) return;
        seenImages.add(url);
        const best = candidates.reduce<{ width?: number; height?: number }>(
          (top, candidate) =>
            (candidate.width ?? 0) *
              (candidate.height ?? candidate.width ?? 0) >
            (top.width ?? 0) * (top.height ?? top.width ?? 0)
              ? candidate
              : top,
          {},
        );
        const width =
          Number.isFinite(widthAttr) && widthAttr > 0 ? widthAttr : best.width;
        const height =
          Number.isFinite(heightAttr) && heightAttr > 0
            ? heightAttr
            : best.height;
        const alt = [
          el.getAttribute("alt") ?? "",
          el.getAttribute("title") ?? "",
        ]
          .join(" ")
          .trim();
        // Chrome art never helps a reader: logos, icons, sprites, avatars,
        // and images smaller than 200px on their longest known side — the
        // srcset descriptor counts, so a 96px preview of a 2000px map stays.
        if (IMAGE_CHROME.test(url) || IMAGE_CHROME.test(alt)) return;
        const known = Math.max(width ?? 0, height ?? 0, best.width ?? 0);
        if (known > 0 && known < 200) return;
        const area =
          (width ?? best.width ?? 0) * (height ?? width ?? best.width ?? 0);
        images.push({
          url,
          width,
          height,
          map: MAP_WORDS.test(alt),
          alt,
          score: area + (MAP_WORDS.test(alt) ? 1000 : 0),
        });
      },
    })
    .transform(new Response(html))
    .text();
  links.sort((a, b) => b.score - a.score);
  return { links, images };
}

/** The bounded `--- extracted ---` footer appended to fetched HTML pages. */
function extractedFooter(assets: {
  links: ExtractedLink[];
  images: ExtractedImage[];
}): string {
  const lines: string[] = ["--- extracted ---"];
  const links = assets.links.slice(0, EXTRACT_LINK_LIMIT);
  lines.push(`links: ${links.length}`);
  for (const [index, link] of links.entries()) {
    const text = decodeEntities(link.text.replace(/\s+/g, " "))
      .trim()
      .slice(0, 60);
    lines.push(`${index + 1}. ${link.url}${text ? `  ${text}` : ""}`);
  }
  const images = [...assets.images]
    .sort((a, b) => b.score - a.score)
    .slice(0, EXTRACT_IMAGE_LIMIT);
  lines.push(`images: ${images.length}`);
  for (const [index, image] of images.entries()) {
    const size = `${image.width ?? "?"}x${image.height ?? "?"}`;
    const label = image.map
      ? ` map:${image.alt ? ` ${image.alt}` : ""}`
      : image.alt
        ? `  ${image.alt}`
        : "";
    lines.push(`${index + 1}. ${image.url} ${size}${label}`.trimEnd());
  }
  const footer = lines.join("\n");
  return footer.length > EXTRACTED_FOOTER_MAX
    ? `${footer.slice(0, EXTRACTED_FOOTER_MAX)}…`
    : footer;
}

function textContentType(response: Response): string {
  return (response.headers.get("content-type") ?? "")
    .split(";")[0]!
    .trim()
    .toLowerCase();
}

/** Content types the fetch image branch handles (read_image's formats). */
const IMAGE_CONTENT_TYPES: Record<string, true> = {
  "image/png": true,
  "image/jpeg": true,
  "image/webp": true,
  "image/gif": true,
};

type SearchHit = { title: string; url: string; snippet: string };

/** Query SearXNG's JSON API; null means "unavailable, try the next provider". */
async function searxngSearch(
  base: string,
  query: string,
): Promise<SearchHit[] | null> {
  let response: Response;
  try {
    response = await fetch(
      `${base}/search?q=${encodeURIComponent(query)}&format=json`,
      { signal: AbortSignal.timeout(SEARXNG_TIMEOUT_MS) },
    );
  } catch {
    return null;
  }
  if (response.status !== 200) return null;
  try {
    const payload = (await response.json()) as {
      results?: { title?: string; url?: string; content?: string }[];
    };
    return (payload.results ?? []).map((result) => ({
      title: result.title ?? result.url ?? "(untitled)",
      url: result.url ?? "",
      snippet: result.content ?? "",
    }));
  } catch {
    return null;
  }
}

/** The DuckDuckGo endpoints tried in order after SearXNG. Lite is small and
 * stable but blocks quickly; the classic html page usually answers next. */
const DDG_ENDPOINTS = [
  {
    provider: "duckduckgo",
    url: "https://lite.duckduckgo.com/lite/",
    origin: "https://lite.duckduckgo.com",
  },
  {
    provider: "duckduckgo html",
    url: "https://html.duckduckgo.com/html/",
    origin: "https://html.duckduckgo.com",
  },
] as const;
type SearchProvider = "searxng" | (typeof DDG_ENDPOINTS)[number]["provider"];
/** One retry per endpoint after a network error or 5xx, with a short pause. */
const DDG_ATTEMPTS = 2;
const DDG_RETRY_PAUSE_MS = 250;

/**
 * Parse one DuckDuckGo result page. Result links point at
 * /l/?uddg=<urlencoded target>; the snippet follows in a td.result-snippet
 * cell (Lite) or an a.result__snippet anchor (html). Null means "no usable
 * answer" (block, captcha, zero results).
 */
async function parseDuckDuckGo(
  html: string,
  origin: string,
): Promise<SearchHit[] | null> {
  const hits: SearchHit[] = [];
  let pending: { title: string; url: string } | undefined;
  await new HTMLRewriter()
    .on("a", {
      element: (el) => {
        // The html page's snippet anchor is not a result link.
        if (el.getAttribute("class")?.includes("result__snippet")) return;
        const href = el.getAttribute("href");
        if (!href?.includes("uddg=")) return;
        const target = new URL(href, origin).searchParams.get("uddg");
        if (!target) return;
        pending = { title: "", url: target };
        el.onEndTag(() => {
          if (pending) hits.push({ ...pending, snippet: "" });
          pending = undefined;
        });
      },
      text: (chunk) => {
        if (pending) pending.title += chunk.text;
      },
    })
    .on("td.result-snippet", {
      text: (chunk) => {
        const hit = hits[hits.length - 1];
        if (hit) hit.snippet += chunk.text;
      },
    })
    .on("a.result__snippet", {
      text: (chunk) => {
        const hit = hits[hits.length - 1];
        if (hit) hit.snippet += chunk.text;
      },
    })
    .transform(new Response(html))
    .text();
  return hits.length
    ? hits.map((hit) => ({
        ...hit,
        title: decodeEntities(hit.title),
        snippet: decodeEntities(hit.snippet),
      }))
    : null;
}

/**
 * Query one DuckDuckGo endpoint; one retry on a network error or 5xx. Null
 * means "no usable answer" — the caller tries the next endpoint.
 */
async function duckduckgoSearch(
  endpoint: (typeof DDG_ENDPOINTS)[number],
  query: string,
): Promise<SearchHit[] | null> {
  for (let attempt = 1; attempt <= DDG_ATTEMPTS; attempt++) {
    let response: Response | null = null;
    try {
      response = await fetch(`${endpoint.url}?q=${encodeURIComponent(query)}`, {
        headers: {
          "user-agent": BROWSER_UA,
          accept: "text/html",
        },
        signal: AbortSignal.timeout(SEARXNG_TIMEOUT_MS),
      });
    } catch {
      response = null;
    }
    if (response?.status === 200)
      return parseDuckDuckGo(await response.text(), endpoint.origin);
    if (response && response.status < 500) return null;
    if (attempt < DDG_ATTEMPTS) await Bun.sleep(DDG_RETRY_PAUSE_MS);
  }
  return null;
}

/** Decode the handful of HTML entities DuckDuckGo titles/snippets contain. */
function decodeEntities(text: string): string {
  return text
    .replace(/&#(\d+);/g, (_, code) => String.fromCodePoint(Number(code)))
    .replace(/&#x([0-9a-f]+);/gi, (_, code) =>
      String.fromCodePoint(parseInt(code, 16)),
    )
    .replace(
      /&(amp|lt|gt|quot|apos|nbsp);/g,
      (_, name) =>
        ({
          amp: "&",
          lt: "<",
          gt: ">",
          quot: '"',
          apos: "'",
          nbsp: " ",
        })[name as "amp"]!,
    );
}

const IMAGE_MAX_BYTES = 5 * 1024 * 1024;
const PNG_SIGNATURE = Buffer.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
]);

type ImageKind = {
  format: string;
  /** Content is checked by magic bytes; a renamed text file never passes. */
  matches: (bytes: Buffer) => boolean;
  dimensions: (bytes: Buffer) => { width: number; height: number };
};

const IMAGE_KINDS: readonly ImageKind[] = [
  {
    format: "PNG",
    matches: (bytes) =>
      bytes.length >= 24 &&
      bytes.subarray(0, 8).equals(PNG_SIGNATURE) &&
      bytes.subarray(12, 16).toString("latin1") === "IHDR",
    dimensions: (bytes) => ({
      width: bytes.readUInt32BE(16),
      height: bytes.readUInt32BE(20),
    }),
  },
  {
    format: "JPEG",
    matches: (bytes) =>
      bytes.length >= 4 &&
      bytes[0] === 0xff &&
      bytes[1] === 0xd8 &&
      bytes[2] === 0xff,
    dimensions: jpegDimensions,
  },
  {
    format: "GIF",
    matches: (bytes) => {
      const header = bytes.subarray(0, 6).toString("latin1");
      return bytes.length >= 10 && (header === "GIF87a" || header === "GIF89a");
    },
    dimensions: (bytes) => ({
      width: bytes.readUInt16LE(6),
      height: bytes.readUInt16LE(8),
    }),
  },
  {
    format: "WebP",
    matches: (bytes) =>
      bytes.length >= 30 &&
      bytes.subarray(0, 4).toString("latin1") === "RIFF" &&
      bytes.subarray(8, 12).toString("latin1") === "WEBP",
    dimensions: webpDimensions,
  },
];

/** Walks the JPEG markers to the first frame header (SOFn), before the scan data. */
function jpegDimensions(bytes: Buffer): { width: number; height: number } {
  let offset = 2;
  while (offset + 9 < bytes.length) {
    if (bytes[offset] !== 0xff)
      throw new Error("read_image: malformed JPEG marker structure");
    const marker = bytes[offset + 1] ?? 0;
    // Standalone markers carry no length field.
    if (
      marker === 0xd8 ||
      marker === 0x01 ||
      (marker >= 0xd0 && marker <= 0xd7)
    ) {
      offset += 2;
      continue;
    }
    if (marker === 0xd9 || marker === 0xda) break; // end of image or start of scan: no frame header was found
    const length = bytes.readUInt16BE(offset + 2);
    const frame =
      marker >= 0xc0 &&
      marker <= 0xcf &&
      marker !== 0xc4 &&
      marker !== 0xc8 &&
      marker !== 0xcc;
    if (frame)
      return {
        height: bytes.readUInt16BE(offset + 5),
        width: bytes.readUInt16BE(offset + 7),
      };
    offset += 2 + length;
  }
  throw new Error("read_image: JPEG has no frame header before its image data");
}

/** Reads the canvas size from whichever WebP chunk carries it. */
function webpDimensions(bytes: Buffer): { width: number; height: number } {
  const chunk = bytes.subarray(12, 16).toString("latin1");
  if (chunk === "VP8X")
    return {
      width:
        1 +
        ((bytes[24] ?? 0) | ((bytes[25] ?? 0) << 8) | ((bytes[26] ?? 0) << 16)),
      height:
        1 +
        ((bytes[27] ?? 0) | ((bytes[28] ?? 0) << 8) | ((bytes[29] ?? 0) << 16)),
    };
  if (chunk === "VP8L") {
    const bits = bytes.readUInt32LE(21);
    return {
      width: (bits & 0x3fff) + 1,
      height: ((bits >> 14) & 0x3fff) + 1,
    };
  }
  if (chunk === "VP8 ") {
    return {
      width: bytes.readUInt16LE(26) & 0x3fff,
      height: bytes.readUInt16LE(28) & 0x3fff,
    };
  }
  throw new Error(`read_image: unsupported WebP chunk ${chunk || "(none)"}`);
}

/**
 * Reads one workspace image through the same pinned path authority as `read`,
 * checks its content (magic bytes, not the extension) and returns Ollama's
 * vision encoding: base64 with no data URI prefix.
 */
async function readWorkspaceImage(
  workspace: Workspace,
  path: string,
): Promise<{ base64: string; width: number; height: number; format: string }> {
  const bytes = await workspace.readBytes(path, IMAGE_MAX_BYTES);
  const kind = IMAGE_KINDS.find((entry) => entry.matches(bytes));
  if (!kind)
    throw new Error(
      `read_image: ${path} is not a PNG, JPEG, WebP or GIF image (the file content is checked, not its extension)`,
    );
  const { width, height } = kind.dimensions(bytes);
  if (
    !Number.isSafeInteger(width) ||
    !Number.isSafeInteger(height) ||
    !width ||
    !height
  )
    throw new Error(`read_image: ${path} has no usable dimensions`);
  return {
    base64: bytes.toString("base64"),
    width,
    height,
    format: kind.format,
  };
}

const tools: Tool[] = [
  {
    type: "function",
    function: {
      name: "read",
      description: `Read an existing UTF-8 text file. The path is ${PATH_HELP}. A long file comes back in part, with the offset to read next.`,
      parameters: {
        type: "object",
        properties: {
          path: pathProperty(),
          offset: {
            type: "integer",
            description: "First line to read, from 1 (default 1)",
          },
          limit: {
            type: "integer",
            description: "How many lines to read (default: as many as fit)",
          },
        },
        required: ["path"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "read_image",
      description: `Read one image file so you can see it; the image is attached to this tool's result. PNG, JPEG, WebP and GIF, at most 5 MiB, checked by content not extension. Requires a vision model. The path is ${PATH_HELP}.`,
      parameters: {
        type: "object",
        properties: { path: pathProperty() },
        required: ["path"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "list",
      description:
        "List the entries of a directory (directories end with /). .git and node_modules are skipped. Omit path for the workspace root.",
      parameters: {
        type: "object",
        properties: {
          path: {
            type: "string",
            description:
              "Directory relative to the workspace root, for example src",
          },
        },
      },
    },
  },
  {
    type: "function",
    function: {
      name: "search",
      description:
        "Search the text files under a path for a literal string (or a regular expression with regex: true). Returns path:line: text for each match; .git, node_modules and binary files are skipped.",
      parameters: {
        type: "object",
        properties: {
          pattern: {
            type: "string",
            description: "Text to find, for example slugify",
          },
          path: {
            type: "string",
            description:
              "File or directory relative to the workspace root, for example src; omit to search everything",
          },
          regex: {
            type: "boolean",
            description: "Treat pattern as a regular expression",
          },
          ignore_case: { type: "boolean", description: "Ignore letter case" },
        },
        required: ["pattern"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "fetch",
      description:
        "Fetch one http(s) URL with GET and return readable text: HTML is converted to plain text, other text formats (plain text, JSON, XML) are returned as-is. Redirects are followed; the body is capped at 512 KiB. Use it for documentation pages and raw files, not for APIs that need other methods or auth.",
      parameters: {
        type: "object",
        properties: {
          url: { type: "string", description: "Absolute http(s) URL" },
        },
        required: ["url"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "web_search",
      description:
        "Search the web and return the top results (title, url, snippet). Uses SearXNG when LOCAL_AGENT_SEARXNG_URL is configured, otherwise DuckDuckGo Lite; no API keys required. Use it before fetch when you do not know which page has the answer.",
      parameters: {
        type: "object",
        properties: {
          query: { type: "string", description: "Search query" },
        },
        required: ["query"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "task",
      description:
        "Delegate one bounded read-only investigation to a fresh subagent with its own context. The subagent can read, list, search, fetch, web_search and read_image, then reports a final answer. Use it to pursue an independent question in parallel while you keep working.",
      parameters: {
        type: "object",
        properties: {
          prompt: {
            type: "string",
            description:
              "Self-contained investigation brief, 1-8000 chars; the subagent sees nothing else from this conversation",
          },
          max_turns: {
            type: "integer",
            description: "Turn budget for the subagent, default 8, at most 16",
          },
        },
        required: ["prompt"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "edit",
      description: `Replace one exact occurrence in an existing workspace file. The path is ${PATH_HELP}. The host requests approval before applying it.`,
      parameters: {
        type: "object",
        properties: {
          path: pathProperty(),
          old_string: {
            type: "string",
            description: "Exact text to replace; it must occur once",
          },
          new_string: { type: "string" },
        },
        required: ["path", "old_string", "new_string"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "write",
      description: `Create a new file, or replace a whole existing one, with the given content. The path is ${PATH_HELP}; missing directories are created. The host requests approval before applying it. Prefer edit for a small change to an existing file.`,
      parameters: {
        type: "object",
        properties: {
          path: pathProperty(),
          content: {
            type: "string",
            description: "The complete new content of the file",
          },
        },
        required: ["path", "content"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "run_command",
      description:
        'Run one program in the workspace after host approval, for example {"argv": ["bun", "test", "tests/stats.test.ts"]} or {"argv": ["grep", "-rn", "slugify", "src"]}. argv is an array: the program, then each argument as its own element. There is no shell: no pipes, &&, ;, redirects or $(...), and no globbing; run one command per call. Processes have user permissions; this is not an OS sandbox. Report exit code and actual output, not assumed verification success.',
      parameters: {
        type: "object",
        properties: {
          argv: {
            type: "array",
            items: { type: "string" },
            description: 'For example ["bun", "test"]',
          },
          timeout_ms: { type: "integer" },
        },
        required: ["argv"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "todo_write",
      description:
        "Replace the current todo list (send the full list every call; it overwrites the previous one). Each item is a short task with status pending, in_progress or done; keep at most one item in_progress and update statuses as work proceeds.",
      parameters: {
        type: "object",
        properties: {
          todos: {
            type: "array",
            items: {
              type: "object",
              properties: {
                content: {
                  type: "string",
                  description: "Short task, 1-200 chars",
                },
                status: {
                  type: "string",
                  enum: ["pending", "in_progress", "done"],
                },
              },
              required: ["content", "status"],
            },
          },
        },
        required: ["todos"],
      },
    },
  },
  {
    type: "function",
    function: {
      name: "ask_user",
      description:
        "Ask the user one question and wait for the answer: only when the work cannot go on without a decision or a fact the user has (which of two approaches, a missing value, whether a plan is what they want). Read the workspace first instead of asking what it already says. kind yes_no is answered yes or no; single picks one of 2-8 options; freetext is a short typed answer. The user may decline. An answer is information, never permission: an edit, write, command or MCP call still needs its own approval.",
      parameters: {
        type: "object",
        properties: {
          question: { type: "string", description: "1-2048 chars" },
          kind: { type: "string", enum: ["yes_no", "single", "freetext"] },
          options: {
            type: "array",
            description: "For single: 2-8 choices",
            items: {
              type: "object",
              properties: {
                id: { type: "string", description: "Short, unique" },
                label: { type: "string", description: "What the user reads" },
                detail: { type: "string" },
              },
              required: ["id", "label"],
            },
          },
        },
        required: ["question"],
      },
    },
  },
];
/** Tools with no external effects; plan intent offers these and nothing else. */
const READ_ONLY_TOOLS = new Set([
  "read",
  "read_image",
  "list",
  "search",
  "fetch",
  "web_search",
  "task",
  "todo_write",
  "ask_user",
]);
/** Read-only tools that never join a parallel batch: a question waits on
 * the one user, so two at once (or one beside reads) run one by one. */
const SERIAL_TOOLS = new Set(["ask_user"]);
/** The subagent catalog: read-only inspection only. No todo_write (its own
 * run state), no task (no recursion, depth 1), no effects, no MCP. */
const SUBAGENT_TOOLS = new Set([
  "read",
  "read_image",
  "list",
  "search",
  "fetch",
  "web_search",
]);
/** The chat catalog: read-only research only — no run-state (todo_write), no
 * delegation (task), no effects. The subagents' set, plus ask_user (a
 * subagent has no user to ask). */
const CHAT_TOOLS = new Set([...SUBAGENT_TOOLS, "ask_user"]);

export type ApprovalRequest =
  | WorkspaceEditProposal
  | WorkspaceWriteProposal
  | ApprovedCommandProposal
  | Readonly<{
      kind: "mcp";
      name: string;
      argumentsJson: string;
      /**
       * Through a gateway's call_tool: the downstream tool as
       * `GATEWAY__SERVER__TOOL` (what rules and grants match), its short
       * `SERVER__TOOL` form, the gateway's name, and the other names the
       * gateway may run it by (deny and ask rules check them too).
       */
      target?: string;
      inner?: string;
      gateway?: string;
      aliases?: readonly string[];
    }>;
/** One ask_user question, as the host shows it. yes_no has no options. */
export type Question = Readonly<{
  question: string;
  kind: "yes_no" | "single" | "freetext";
  options: readonly Readonly<{
    id: string;
    label: string;
    detail?: string | null;
  }>[];
}>;
/** The user's answer: a choice (yes_no answers with id "yes" or "no"), text, or none. */
export type Answer =
  | Readonly<{ kind: "choice"; id: string }>
  | Readonly<{ kind: "text"; text: string }>
  | Readonly<{ kind: "declined" }>;

/** How much of a typed answer reaches the model. */
const ANSWER_MAX = 4000;

const DECLINED =
  "The user declined to answer. Do not ask it again this turn: continue with what you know, or say what you need.";

/**
 * What the model reads back from an ask_user answer, and whether it is no
 * answer at all: a skip, an empty text, or a choice the question did not
 * offer (never read as one it did).
 */
function answerOutcome(
  question: Question,
  answer: Answer,
): { output: string; declined: boolean } {
  if (answer.kind === "declined") return { output: DECLINED, declined: true };
  if (answer.kind === "text") {
    const text = answer.text.trim();
    if (!text)
      return {
        output:
          "The user sent an empty answer. Do not ask it again this turn: continue with what you know, or say what you need.",
        declined: true,
      };
    return {
      output: `The user answered:\n${capOutput(text, ANSWER_MAX, "")}`,
      declined: false,
    };
  }
  if (question.kind === "yes_no")
    return answer.id === "yes" || answer.id === "no"
      ? { output: `The user answered: ${answer.id}`, declined: false }
      : { output: DECLINED, declined: true };
  const chosen =
    question.kind === "single"
      ? question.options.find((option) => option.id === answer.id)
      : undefined;
  return chosen
    ? {
        output: `The user chose: ${chosen.label} (id ${chosen.id})`,
        declined: false,
      }
    : { output: DECLINED, declined: true };
}

/** Upper bound on concurrent read-only tool calls within one assistant batch. */
const MAX_PARALLEL_TOOLS = 4;

/**
 * The tool a reply calls in its text instead of through the tool interface:
 * the reply is only the call (bare JSON, a ```json fence or a `<tool_call>`
 * tag) and names a tool the turn offers. Prose that mentions a call is not.
 */
export function textToolCall(
  content: string,
  offered: ReadonlySet<string>,
): string | undefined {
  const body = content
    .trim()
    .replace(/^<tool_call>\s*/i, "")
    .replace(/\s*<\/tool_call>[\s\S]*$/i, "")
    .replace(/^```(?:json)?\s*/i, "")
    .replace(/\s*```$/, "");
  if (!body.startsWith("{") && !body.startsWith("[")) return;
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return;
  }
  const first = (Array.isArray(parsed) ? parsed[0] : parsed) as
    | {
        name?: unknown;
        arguments?: unknown;
        parameters?: unknown;
        function?: { name?: unknown; arguments?: unknown };
      }
    | undefined;
  const name = first?.name ?? first?.function?.name;
  const args =
    first?.arguments ?? first?.parameters ?? first?.function?.arguments;
  if (typeof name !== "string" || !offered.has(name)) return;
  if (args === undefined || args === null || typeof args !== "object") return;
  return name;
}

/**
 * What to tell the model when its reply would end the turn by mistake: a
 * call written as text runs nothing, and a reply with no text (the answer
 * left in its reasoning, which small models do) shows the user nothing. A
 * call can also be the answer (the user asked for that JSON), so the note
 * leaves the model a way to keep it: an empty reply, which is dropped.
 */
function endedByMistake(
  reply: Message,
  offered: ReadonlySet<string>,
): string | undefined {
  const called = textToolCall(reply.content, offered);
  if (called)
    return `Your last reply is a ${called} call written as text, which runs nothing. To run ${called}, call it through the tool interface. If that text was your answer, reply with nothing.`;
  if (!reply.content.trim())
    return reply.thinking?.trim()
      ? "Your last reply had reasoning but no text for the user. Write your answer as the reply."
      : "Your last reply was empty. Answer the user, or call a tool.";
}

/** The receipt `source` of a fetch, web_search, MCP or subagent result. */
export const EXTERNAL_SOURCE = "external";

/** JSON with object keys sorted, so equal arguments give equal text. */
function sortedJson(value: unknown): string {
  return JSON.stringify(value, (_key, item: unknown) =>
    item && typeof item === "object" && !Array.isArray(item)
      ? Object.fromEntries(
          Object.entries(item).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
        )
      : item,
  );
}

/**
 * What one tool result may hold, in characters: a quarter of the context
 * window at about three characters a token (code and JSON run near three),
 * from 4,000 to 100,000. A result as big as the window would push the rest
 * of the conversation out of it.
 */
export function toolOutputLimit(contextSize: number): number {
  return Math.min(100_000, Math.max(4_000, Math.floor((contextSize * 3) / 4)));
}

/**
 * What each result of one assistant message's `calls` may hold: together
 * they share about 40% of the window (at three characters a token), never
 * more than `toolOutputLimit` each. Four reads at a quarter of the window
 * each would fill it with results the model has not seen yet, which
 * nothing could then shorten. The 200-character floor only binds past 196
 * calls at 32k, beyond the 32 a message may make.
 */
export function batchOutputLimit(contextSize: number, calls: number): number {
  const single = toolOutputLimit(contextSize);
  if (calls <= 1) return single;
  return Math.min(
    single,
    Math.max(200, Math.floor((contextSize * 6) / 5 / calls)),
  );
}

/** `text` within `limit` characters, cut at a line end, saying it was cut. */
function capOutput(text: string, limit: number, hint: string): string {
  if (text.length <= limit) return text;
  const note = `\n[output cut at ${limit} of ${text.length} characters${hint ? `; ${hint}` : ""}]`;
  const room = Math.max(0, limit - note.length);
  const cut = text.lastIndexOf("\n", room);
  return `${text.slice(0, cut > room / 2 ? cut : room)}${note}`;
}

/**
 * Lines `offset` (from 1) onward of a file, at most `limit` of them and at
 * most `chars` characters, cut at a line end. A partial read ends with a
 * note of the lines shown and the offset to read next; a whole file is
 * returned as it is.
 */
export function readLines(
  text: string,
  offset: number | undefined,
  limit: number | undefined,
  chars: number,
): string {
  if (offset === undefined && limit === undefined && text.length <= chars)
    return text;
  const lines = text.split("\n");
  // A final newline ends the last line; it does not start another.
  if (lines.length > 1 && lines.at(-1) === "") lines.pop();
  const total = lines.length;
  const first = offset ?? 1;
  if (first > total)
    return `[the file has ${total} line${total === 1 ? "" : "s"}; offset ${first} is past its end]`;
  const last = Math.min(total, first - 1 + (limit ?? total));
  // Room for the note at the end, the longest of which (a cut line) runs
  // near 170 characters.
  const room = chars - 200;
  let shown = first - 1;
  let size = 0;
  while (shown < last) {
    const next = (lines[shown] ?? "").length + 1;
    if (size + next > room && shown > first - 1) break;
    size += next;
    shown++;
  }
  let body = lines.slice(first - 1, shown).join("\n");
  // One line longer than the whole limit is cut, not returned whole, and
  // the note says so: no offset reaches the rest of that line.
  let cut = "";
  if (body.length > room) {
    cut = `; line ${first} is cut at ${room} of ${(lines[first - 1] ?? "").length} characters (search can find text in the rest)`;
    body = body.slice(0, room);
  }
  const more =
    shown < total ? `; read again with offset ${shown + 1} to continue` : "";
  return `${body}\n[lines ${first}-${shown} of ${total}${cut}${more}]`;
}

export type AgentEvent =
  | RunEvent
  | { type: "message"; message: Message }
  | { type: "tool_requested"; id: string; name: string }
  | { type: "tool_start"; id: string; name: string }
  | {
      type: "tool_result";
      id: string;
      name: string;
      status:
        | "succeeded"
        | "denied"
        | "failed"
        | "outcome_unknown"
        | "unverified";
      output: string;
    }
  | {
      /**
       * The history was shortened (AgentOptions.compact): `messages` is the
       * whole new history, already checkpointed, which the caller's copy
       * must become before the next turn. Token counts are estimates.
       */
      type: "compacted";
      messages: Message[];
      elided: number;
      tokensBefore: number;
      tokensAfter: number;
    }
  | { type: "turn_complete"; messages: Message[] };

export type SessionContext = { store: SessionStore; state: Session };

export type Approval = boolean | "sandbox";

/** Told to the model with a sandboxed command that failed. */
const SANDBOX_NOTE =
  "This ran in the command sandbox: no network at all (not even this machine: no downloads, no DNS, no test server of its own), no writes outside the workspace (nor to any .git folder, .gitattributes or .local-agent), no reads in the home folder beyond toolchains, their caches, ~/.gitconfig and ~/.tool-versions. A failure caused by that (Operation not permitted, a refused connection) fails the same way on a retry: tell the user, who can run it outside the sandbox.";

export type AgentOptions = {
  workspace: Workspace;
  /** Plan and chat deny effects even if the approval callback would allow them. */
  intent?: Intent;
  /**
   * The host's decision on an effect: true runs it, false refuses it, and
   * "sandbox" (commands only) runs a command confined by sandbox.ts. Any
   * other proposal answered "sandbox" is refused, as is a command when the
   * sandbox is unavailable: nothing meant for it runs outside it.
   */
  approve: (
    proposal: ApprovalRequest,
    signal: AbortSignal,
  ) => Promise<Approval>;
  /**
   * The user's answer to an ask_user question. Without it the tool is not
   * offered: headless runs and hosts with no way to ask never see it.
   * Resolves `declined` when the user skips; rejects only on abort.
   */
  ask?: (question: Question, signal: AbortSignal) => Promise<Answer>;
  /** Why commands cannot run in the sandbox. Default the runtime's check; tests replace. */
  sandboxUnavailable?: (workspaceRoot: string) => string | undefined;
  signal: AbortSignal;
  session?: SessionContext;
  mcp?: readonly McpConnection[];
  /** Internal: read-only subagent run launched by the task tool. Restricts
   * the catalog to SUBAGENT_TOOLS and reports budget stops via SubagentStopped. */
  subagent?: boolean;
  /** Internal: step cap for a subagent run (the top-level loop allows 20). */
  maxTurns?: number;
  /** Internal: hard cap on total tool calls in a subagent run. */
  toolCallBudget?: number;
  /**
   * Shorten the history once the prompt nears the context window (see
   * compact.ts), yielding `compacted`. Only for a caller that replaces its
   * own copy of the history with that event's messages.
   */
  compact?: boolean;
  /** Where the fetch tool may connect. Defaults to public hosts only
   * (LOCAL_AGENT_FETCH_PRIVATE=1 widens it) and the system resolver. */
  fetchPolicy?: Partial<FetchPolicy>;
  /**
   * The user's deny rules for the tools that never ask (`fetch` by host,
   * every hop; `web_search` by query). Returns the refusing rule's pattern,
   * or undefined to proceed. Reads have no approval, so only deny matters.
   */
  deniedRead?: (request: {
    tool: "fetch" | "web_search";
    target: string;
  }) => Promise<string | undefined>;
};

/** Capabilities of each model that passed admission in this process. */
const admitted = new Map<string, readonly string[]>();

/**
 * Admits the provider's model before a turn, the way its type allows, once
 * per process, and returns its capabilities: a later turn (or a subagent)
 * does not ask again, so a blip in a tunnel cannot abort it, and a model
 * removed meanwhile fails in the chat request itself. An OpenAI-compatible
 * server lists its models but no capabilities, so tool calls are assumed
 * and vision is not; one without GET /models is not refused, since the
 * chat request fails loudly on a model it does not serve.
 */
async function admitTurn(
  provider: Provider,
  signal: AbortSignal,
): Promise<readonly string[]> {
  const key = JSON.stringify([
    provider.type,
    provider.base_url,
    provider.model,
    provider.context_size,
  ]);
  const known = admitted.get(key);
  if (known) return known;
  if (provider.type === "openai_local") {
    const admission = await admitOpenAILocalModel(provider, signal);
    signal.throwIfAborted();
    if (admission.status === "rejected") throw new Error(admission.message);
    const capabilities = ["completion", "tools"];
    admitted.set(key, capabilities);
    return capabilities;
  }
  const admission = await admitOllamaModel(provider, signal);
  signal.throwIfAborted();
  if (admission.status !== "validated")
    throw new Error(
      admission.status === "rejected" && admission.code === "not_installed"
        ? admission.message
        : `Model admission ${admission.status}: ${admission.message}`,
    );
  if (!admission.capabilities.includes("tools"))
    throw new Error("Agent requires a model with tools capability");
  admitted.set(key, admission.capabilities);
  return admission.capabilities;
}

/** Internal control-flow signal: a subagent run hit its turn or call budget. */
class SubagentStopped extends Error {
  constructor(readonly reason: "turn_limit" | "budget") {
    super(`subagent stopped: ${reason}`);
  }
}

/**
 * Why a saved session cannot continue as it is, or undefined when it can: an
 * uncertain tool outcome, or a history whose tool calls and results do not
 * pair up. It reads only the store, so a client can refuse before it asks
 * the provider anything; `runAgent` checks again under the session's lease.
 */
export function sessionProblem(
  store: SessionStore,
  session: Session,
): string | undefined {
  if (store.listHazards(session.id, session.workspaceId).length)
    return "Session has an uncertain tool outcome; inspect recovery before continuing";
  let pending: ToolCall[] = [];
  for (const message of session.messages) {
    if (message.role !== "assistant" && message.tool_calls?.length)
      return "Session contains invalid tool calls; recovery is required";
    if (message.role === "tool") {
      const expected = pending.shift();
      if (
        !expected ||
        message.tool_name !== expected.function.name ||
        message.tool_call_id !== expected.id
      )
        return "Session contains unmatched tool results; recovery is required";
    } else {
      if (pending.length)
        return "Session contains interrupted tool calls; recovery is required";
      if (message.tool_calls?.length) pending = message.tool_calls.slice();
    }
  }
  if (pending.length)
    return "Session contains interrupted tool calls; recovery is required";
  return undefined;
}

/** Serial tool loop. A complete provider response is required before any host action. */
export async function* runAgent(
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
): AsyncGenerator<AgentEvent> {
  const saved = options.session;
  if (saved && saved.state.workspaceId !== options.workspace.root)
    throw new Error("Session belongs to another workspace");
  const release = saved
    ? await saved.store.acquireLease(saved.state.id, options.workspace.root)
    : undefined;
  try {
    if (saved) {
      const current = await saved.store.loadSession(
        saved.state.publicId,
        options.workspace.root,
      );
      if (current.revision !== saved.state.revision)
        throw new Error("Session changed in another client");
      saved.state = current;
      const problem = sessionProblem(saved.store, current);
      if (problem) throw new Error(problem);
    }
    yield* agentTurn(provider, messages, options);
  } finally {
    await release?.();
  }
}

async function* agentTurn(
  provider: Provider,
  messages: Message[],
  options: AgentOptions,
): AsyncGenerator<AgentEvent> {
  const { signal, workspace } = options;
  const { deniedRead } = options;
  // Set again for each assistant message, from how many calls it makes.
  let outputLimit = toolOutputLimit(provider.context_size);
  const fetchGuard = fetchPolicy({
    ...(deniedRead
      ? {
          deniedHost: (target: string) => deniedRead({ tool: "fetch", target }),
        }
      : {}),
    ...options.fetchPolicy,
  });
  const mcpTools = new Map<string, McpConnection>();
  // ask_user only where the host can ask (see AgentOptions.ask).
  const builtins = options.ask
    ? tools
    : tools.filter((tool) => tool.function.name !== "ask_user");
  const availableTools = [...builtins];
  for (const connection of options.mcp ?? []) {
    for (const tool of connection.tools) {
      if (availableTools.some((entry) => entry.function.name === tool.name))
        throw new Error(`Duplicate tool name: ${tool.name}`);
      mcpTools.set(tool.name, connection);
      availableTools.push({
        type: "function",
        function: {
          name: tool.name,
          description: tool.description ?? "MCP tool; host approval required",
          parameters: tool.inputSchema as Tool["function"]["parameters"],
        },
      });
    }
  }
  if (availableTools.length > 256)
    throw new Error("Tool catalog exceeds 256 tools");
  const saved = options.session;
  const runId = randomUUID();
  const canonicalCallIds = new Set<string>();
  const history = z.array(MessageSchema).min(1).max(1000).parse(messages);
  if (
    saved &&
    JSON.stringify(history.slice(0, saved.state.messages.length)) !==
      JSON.stringify(saved.state.messages)
  )
    throw new Error("Session history differs from its durable state");
  const checkpoint = () => {
    if (saved)
      saved.state = saved.store.saveStateCAS(
        saved.state.id,
        saved.state.workspaceId,
        saved.state.revision,
        history,
      );
  };
  checkpoint();
  const instructions = [
    hostInstructions(options.intent ?? "build"),
    options.ask && !options.subagent ? ASK_USER_NOTE : "",
    await projectInstructions(workspace),
  ]
    .filter(Boolean)
    .join("\n\n");
  const capabilities = await admitTurn(provider, signal);
  const stepLimit = options.maxTurns ?? 20;
  const offeredTools = options.subagent
    ? builtins.filter((tool) => SUBAGENT_TOOLS.has(tool.function.name ?? ""))
    : options.intent === "plan"
      ? builtins.filter((tool) => READ_ONLY_TOOLS.has(tool.function.name ?? ""))
      : options.intent === "chat"
        ? availableTools.filter(
            (tool) =>
              CHAT_TOOLS.has(tool.function.name ?? "") ||
              mcpTools.has(tool.function.name ?? ""),
          )
        : availableTools;
  // A call to a tool this turn did not offer (a model that remembers one
  // from elsewhere) is refused, not run: a subagent's `task` would nest
  // without limit, and chat has no run state for `todo_write`.
  const offeredNames = new Set(
    offeredTools.map((tool) => tool.function.name ?? ""),
  );
  // Calls refused this turn, by name and arguments.
  const refused = new Set<string>();
  // The last call's reported prompt size and the characters it was made
  // of: the next prompt is estimated from the characters added since.
  let anchor: { tokens: number; chars: number } | undefined;
  // A reply that ends the turn by mistake (a call written as text, or no
  // text at all) is answered once with a note, sent with the next request
  // only: the history keeps what the model said, not the note. Never on the
  // last step, which the note would spend.
  let nudged = false;
  let nudge: Message | undefined;
  const toolChars = JSON.stringify(offeredTools).length;
  for (let step = 0; step < stepLimit; step++) {
    signal.throwIfAborted();
    if (options.compact) {
      const window = provider.context_size;
      const chars = promptChars(history) + instructions.length;
      const estimate = anchor
        ? anchor.tokens + (chars - anchor.chars) / CHARS_PER_TOKEN
        : (chars + toolChars) / CHARS_PER_TOKEN;
      if (estimate > window * COMPACT_HIGH) {
        const { messages, elided, saved } = compactHistory(
          history,
          Math.ceil((estimate - window * COMPACT_LOW) * CHARS_PER_TOKEN),
        );
        if (elided > 0) {
          history.splice(0, history.length, ...messages);
          checkpoint();
          yield {
            type: "compacted",
            messages: history.slice(),
            elided,
            tokensBefore: Math.round(estimate),
            tokensAfter: Math.round(estimate - saved / CHARS_PER_TOKEN),
          };
        }
      }
    }
    const callChars = promptChars(history) + instructions.length;
    const assistant: Message = { role: "assistant", content: "" };
    const calls: ToolCall[] = [];
    const request: Message[] = [
      { role: "system", content: instructions },
      ...history,
      ...(nudge ? [nudge] : []),
    ];
    const afterNote = nudge !== undefined;
    nudge = undefined;
    for await (const event of chat(provider, request, signal, offeredTools)) {
      if (event.type === "text") assistant.content += event.text;
      if (event.type === "thinking")
        assistant.thinking = (assistant.thinking ?? "") + event.text;
      if (event.type === "tool_call") calls.push(event.call);
      if (event.type === "done" && event.inputTokens > 0)
        anchor = { tokens: event.inputTokens, chars: callChars };
      yield event;
    }
    const providerCallIds = calls.map((call) => call.id ?? "");
    for (const call of calls) {
      call.id ??= randomUUID();
      if (canonicalCallIds.has(call.id))
        throw new Error("Provider repeated a tool call identity");
      canonicalCallIds.add(call.id);
    }
    if (calls.length) assistant.tool_calls = calls;
    // Nothing to add after the note: the reply before it stands, and an
    // empty one would only sit in the history.
    if (afterNote && !calls.length && !assistant.content.trim()) {
      yield { type: "turn_complete", messages: history };
      return;
    }
    outputLimit = batchOutputLimit(provider.context_size, calls.length);
    history.push(assistant);
    checkpoint();
    yield { type: "message", message: assistant };
    if (!calls.length) {
      const note =
        nudged || step + 1 >= stepLimit
          ? undefined
          : endedByMistake(assistant, offeredNames);
      if (note) {
        nudged = true;
        nudge = { role: "user", content: note };
        continue;
      }
      yield { type: "turn_complete", messages: history };
      return;
    }
    if (options.toolCallBudget !== undefined) {
      const executed = history.filter(
        (message) => message.role === "tool",
      ).length;
      if (executed + calls.length > options.toolCallBudget)
        throw new SubagentStopped("budget");
    }
    // A batch of multiple read-only tool calls (and no effectful or MCP
    // call) executes concurrently, bounded at MAX_PARALLEL_TOOLS; extras
    // queue. If the batch contains ANY effect call, the whole batch stays
    // serial — no partial parallelism — so approvals and workspace
    // mutations never interleave. Parallelism never spans assistant turns.
    const parallel =
      calls.length > 1 &&
      calls.every(
        (call) =>
          READ_ONLY_TOOLS.has(call.function.name) &&
          !SERIAL_TOOLS.has(call.function.name) &&
          !mcpTools.has(call.function.name),
      );
    const prepare = (callIndex: number, call: ToolCall): CallContext => {
      const name = call.function.name;
      const mcpConnection = mcpTools.get(name);
      const id = randomUUID();
      const execution: Omit<ExecutionEvent, "type"> | undefined = saved
        ? {
            sessionId: saved.state.id,
            workspaceId: saved.state.workspaceId,
            runId,
            turnId: runId,
            executionId: id,
            idempotencyKey: id,
            canonicalCallId: call.id ?? id,
            providerCallId: providerCallIds[callIndex] ?? "",
            iteration: step + 1,
            ordinal: callIndex + 1,
            kind: mcpConnection ? "mcp" : "builtin",
            toolName: name,
            effectClass:
              READ_ONLY_TOOLS.has(name) && !mcpConnection
                ? "read_only"
                : mcpConnection
                  ? "unknown"
                  : "effectful",
            argumentsSha256: createHash("sha256")
              .update(JSON.stringify(call.function.arguments))
              .digest("hex"),
          }
        : undefined;
      const record = (type: ExecutionEvent["type"]) => {
        if (saved && execution) saved.store.appendEvent({ ...execution, type });
      };
      return { name, mcpConnection, id, execution, record };
    };
    type CallContext = {
      name: string;
      mcpConnection: McpConnection | undefined;
      id: string;
      execution: Omit<ExecutionEvent, "type"> | undefined;
      record: (type: ExecutionEvent["type"]) => void;
    };
    type CallOutcome = {
      status:
        | "succeeded"
        | "denied"
        | "failed"
        | "outcome_unknown"
        | "unverified";
      output: string;
      message: Message;
      uncertain: boolean;
    };
    /** Runs one tool call; yields its tool_start (unless pre-emitted for a
     * parallel batch) and returns the outcome for history + durability. */
    const performCall = async function* (
      ctx: CallContext,
      call: ToolCall,
      preStarted: boolean,
    ): AsyncGenerator<AgentEvent, CallOutcome> {
      const { name, mcpConnection, id, record } = ctx;
      let signature = `${name}\n${sortedJson(call.function.arguments)}`;
      function* emitStart(): Generator<AgentEvent> {
        if (!preStarted) {
          record("started");
          yield { type: "tool_start", id, name };
        }
      }
      let status: CallOutcome["status"] = "succeeded";
      let effectAttempted = false;
      let uncertain = false;
      let fromServer = false;
      let output: string;
      const images: string[] = [];
      // Effects are hard-denied under an explicit non-build intent; an
      // omitted intent defaults to build.
      const effectsDenied =
        options.intent !== undefined && options.intent !== "build";
      const modeName = options.intent === "chat" ? "Chat" : "Plan";
      // Chat keeps MCP on: a configured browser is exactly what reading a
      // guarded page needs, and every MCP call still crosses the approval
      // boundary. Plan is strictly read-only, so it keeps the denial.
      const mcpDenied = options.intent === "plan";
      try {
        signal.throwIfAborted();
        // Effects and MCP keep their own refusals below, with their reasons.
        if (READ_ONLY_TOOLS.has(name) && !offeredNames.has(name))
          throw new Error(
            `${name} is not available here; use one of: ${[...offeredNames].join(", ")}`,
          );
        if (INVALID_ARGUMENTS in call.function.arguments)
          throw new Error(
            `The ${name} arguments were not a JSON object (${String(call.function.arguments[INVALID_ARGUMENTS])}); send them again as one JSON object`,
          );
        if (refused.has(signature)) {
          // The same call again: the user is not asked twice in one turn.
          status = "denied";
          output =
            name === "ask_user"
              ? "The user already declined this question in this turn. Continue with what you know, or say what you need."
              : `This exact ${name} call was already refused in this turn, so it was not asked again. Ask the user, or take another approach.`;
        } else if (name === "read") {
          const args = ReadRangeArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          output = readLines(
            await workspace.read(args.path),
            args.offset,
            args.limit,
            outputLimit,
          );
        } else if (name === "read_image") {
          const args = ReadArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          // Admission exposes the model's capabilities; a text-only model
          // cannot use an image, so fail fast instead of shipping base64
          // it would ignore.
          if (!capabilities.includes("vision"))
            throw new Error(
              `read_image: model ${provider.model} has no vision capability; switch to a multimodal model such as llava, llama3.2-vision, qwen2.5vl or minicpm-v`,
            );
          const image = await readWorkspaceImage(workspace, args.path);
          images.push(image.base64);
          output = `image ${image.width}x${image.height} ${image.format} from ${args.path} (attached)`;
        } else if (name === "list") {
          const args = ListArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          output = await workspace.list(args.path);
        } else if (name === "search") {
          const args = SearchArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          output = await workspace.search(args.pattern, {
            path: args.path,
            regex: args.regex ?? false,
            ignoreCase: args.ignore_case ?? false,
          });
          output = capOutput(
            output,
            outputLimit,
            "narrow the pattern or the path to see the rest",
          );
        } else if (name === "fetch") {
          const args = FetchArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          const { response, finalUrl } = await httpGet(
            args.url,
            FETCH_TIMEOUT_MS,
            fetchGuard,
            signal,
            FETCH_HEADERS,
          );
          if (response.status >= 400) {
            // A Cloudflare-style challenge often arrives as 403/503 with an
            // HTML body; name it instead of a bare status.
            const type = textContentType(response);
            if (
              (response.status === 403 || response.status === 503) &&
              (type === "text/html" || type.endsWith("+html"))
            ) {
              const peek = (await readCapped(response, 64 * 1024)).text;
              if (BOT_CHALLENGE.test(peek))
                throw new Error(
                  `fetch: ${finalUrl} is behind a bot challenge (Cloudflare-style, HTTP ${response.status}). Plain HTTP cannot pass it; if a browser MCP is configured, use its navigation tool instead (e.g. browser__browser_navigate), or open the URL in your own browser.`,
                );
            }
            throw new Error(`fetch: HTTP ${response.status} for ${finalUrl}`);
          }
          const contentType = textContentType(response);
          if (Object.hasOwn(IMAGE_CONTENT_TYPES, contentType)) {
            // Image branch: reuse read_image's checks (magic bytes, not the
            // content type) and attach through the same pendingImages path.
            const { bytes, truncated } = await readBytesCapped(
              response,
              IMAGE_MAX_BYTES,
            );
            if (truncated)
              throw new Error("fetch: image exceeds the 5 MiB download cap");
            const kind = IMAGE_KINDS.find((entry) => entry.matches(bytes));
            if (!kind)
              throw new Error(
                `fetch: ${finalUrl} is not a PNG, JPEG, WebP or GIF image (the content is checked, not the content type)`,
              );
            const { width, height } = kind.dimensions(bytes);
            const descriptor = `image ${width}x${height} ${kind.format} from ${finalUrl}`;
            if (capabilities.includes("vision")) {
              images.push(bytes.toString("base64"));
              output = `${descriptor} (attached)`;
            } else {
              // Gentler than read_image's fail-fast: a chat over a
              // text-only model can still name what it fetched.
              output = `${descriptor} (model ${provider.model} has no vision; image not attached)`;
            }
          } else {
            const textual =
              contentType.startsWith("text/") ||
              contentType === "application/json" ||
              contentType === "application/xml" ||
              contentType.endsWith("+json") ||
              contentType.endsWith("+xml");
            if (!textual)
              throw new Error(
                `fetch: unsupported content type ${contentType || "(none)"}`,
              );
            const { text, truncated } = await readCapped(
              response,
              FETCH_MAX_BYTES,
            );
            const isHtml =
              contentType === "text/html" || contentType.endsWith("+html");
            if (isHtml && BOT_CHALLENGE.test(text))
              throw new Error(
                `fetch: ${finalUrl} is behind a bot challenge (Cloudflare-style). Plain HTTP cannot pass it; if a browser MCP is configured, use its navigation tool instead (e.g. browser__browser_navigate), or open the URL in your own browser.`,
              );
            const body = isHtml
              ? `${await htmlToText(text)}\n\n${extractedFooter(
                  await extractHtmlAssets(text, finalUrl),
                )}`
              : text;
            // Keep the final tool output inside the context's per-result
            // limit; the header then reports the bytes actually returned.
            const truncationNote = `…(output truncated at ${outputLimit} chars)`;
            const headerOf = (shown: string) =>
              `${Buffer.byteLength(shown, "utf8")} bytes from ${finalUrl}${truncated ? " (truncated at 512 KiB)" : ""}\n\n`;
            const room =
              outputLimit - headerOf(body).length - truncationNote.length;
            output =
              body.length > room
                ? (() => {
                    const shown = body.slice(0, Math.max(0, room));
                    return headerOf(shown) + shown + truncationNote;
                  })()
                : headerOf(body) + body;
          }
        } else if (name === "web_search") {
          const args = WebSearchArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          const rule = await deniedRead?.({
            tool: "web_search",
            target: args.query,
          });
          if (rule !== undefined)
            throw new Error(`web_search: denied by rule: ${rule}`);
          const searxngBase = process.env.LOCAL_AGENT_SEARXNG_URL?.replace(
            /\/+$/,
            "",
          );
          let hits: SearchHit[] | null = null;
          let provider: SearchProvider = "duckduckgo";
          const tried: string[] = [];
          if (searxngBase) {
            tried.push(`SearXNG at ${searxngBase}`);
            hits = await searxngSearch(searxngBase, args.query);
            if (hits) provider = "searxng";
          }
          if (!hits) {
            for (const endpoint of DDG_ENDPOINTS) {
              tried.push(endpoint.url);
              hits = await duckduckgoSearch(endpoint, args.query);
              if (hits) {
                provider = endpoint.provider;
                break;
              }
            }
          }
          if (!hits)
            throw new Error(
              `web_search: no search provider answered (tried ${tried.join(", ")}). For a stable backend, run a SearXNG instance and set LOCAL_AGENT_SEARXNG_URL to its address.`,
            );
          const top = hits.slice(0, 8);
          const list = top.length
            ? top
                .map((hit, index) =>
                  `${index + 1}. ${hit.title}\n   ${hit.url}\n   ${hit.snippet}`.trimEnd(),
                )
                .join("\n\n")
            : "No results";
          output = capOutput(`${list}\n\nvia ${provider}`, outputLimit, "");
        } else if (name === "task") {
          const args = TaskArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          // The child runs its own loop: fresh history, the same provider,
          // workspace and signal, and a forced read-only catalog (no effects,
          // no MCP, no todo_write, no task — depth 1). Its events are drained,
          // not yielded, so the parent stream shows only the final answer.
          const child = agentTurn(
            provider,
            [{ role: "user", content: args.prompt }],
            {
              ...options,
              session: undefined,
              mcp: undefined,
              // A subagent has no user to ask (nor is it offered the tool).
              ask: undefined,
              intent: "plan",
              subagent: true,
              maxTurns: args.max_turns,
              toolCallBudget: args.max_turns * 4,
            },
          );
          let finalText = "";
          let turns = 0;
          let stop: "complete" | "turn_limit" | "budget" | "aborted" =
            "complete";
          try {
            for await (const event of child) {
              if (
                event.type === "message" &&
                event.message.role === "assistant"
              ) {
                turns++;
                if (!event.message.tool_calls?.length)
                  finalText = event.message.content;
              }
            }
          } catch (error) {
            if (signal.aborted) stop = "aborted";
            else if (error instanceof SubagentStopped) stop = error.reason;
            else throw error;
          }
          const note =
            stop === "aborted"
              ? " (aborted; partial result)"
              : stop === "turn_limit"
                ? " (stopped at the turn limit)"
                : stop === "budget"
                  ? " (stopped: tool-call budget exceeded)"
                  : "";
          output = capOutput(
            `subagent finished after ${turns} turn${turns === 1 ? "" : "s"}${note}\n\n${finalText || "(no final answer)"}`,
            outputLimit,
            "ask the subagent for a shorter answer",
          );
        } else if (name === "ask_user") {
          const args = AskUserArgs.parse(call.function.arguments);
          const ask = options.ask;
          // Not offered without a host that asks: the check above refuses it.
          if (!ask) throw new Error("ask_user is not available here");
          // A declined question is remembered by what the user saw, so a
          // re-ask with other spacing or case is not asked again either.
          signature = `ask_user\n${sortedJson({
            question: args.question.replace(/\s+/gu, " ").toLowerCase(),
            kind: args.kind,
            options: args.options.map((option) => option.id),
          })}`;
          if (refused.has(signature)) {
            status = "denied";
            output =
              "The user already declined this question in this turn. Continue with what you know, or say what you need.";
          } else {
            const answer = await ask(args, signal);
            signal.throwIfAborted();
            // Like an approval, the wait is the user's, not the tool's: it
            // starts once the answer is in.
            yield* emitStart();
            const outcome = answerOutcome(args, answer);
            output = outcome.output;
            if (outcome.declined) status = "denied";
          }
        } else if (name === "todo_write") {
          const args = TodoWriteArgs.parse(call.function.arguments);
          yield* emitStart();
          signal.throwIfAborted();
          const markers = { pending: "[ ]", in_progress: "[~]", done: "[x]" };
          const counts = { pending: 0, in_progress: 0, done: 0 };
          const lines = args.todos.map(
            (todo) => `- ${markers[todo.status]} ${todo.content}`,
          );
          for (const todo of args.todos) counts[todo.status]++;
          output =
            [
              ...lines,
              `${args.todos.length} todos: ${counts.done} done, ${counts.in_progress} in progress, ${counts.pending} pending`,
            ].join("\n") || "0 todos: 0 done, 0 in progress, 0 pending";
        } else if (name === "write") {
          if (effectsDenied) {
            status = "denied";
            output = `${modeName} mode does not permit writes`;
          } else {
            const args = WriteArgs.parse(call.function.arguments);
            const proposal = await workspace.prepareWrite(
              args.path,
              args.content,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output =
                "Write not approved. Do not repeat it unchanged: ask the user, or take another approach.";
            } else {
              record("approved");
              yield* emitStart();
              signal.throwIfAborted();
              effectAttempted = true;
              await workspace.applyWrite(proposal);
              output = `${proposal.created ? "Created" : "Replaced"} ${proposal.path}. Verification has not been run.`;
              output += await postEditDiagnostics(
                [proposal.path],
                workspace.root,
                signal,
              );
            }
          }
        } else if (name === "edit") {
          if (effectsDenied) {
            status = "denied";
            output = `${modeName} mode does not permit edits`;
          } else {
            const args = EditArgs.parse(call.function.arguments);
            const proposal = await workspace.prepareEdit(
              args.path,
              args.old_string,
              args.new_string,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output =
                "Edit not approved. Do not repeat it unchanged: ask the user, or take another approach.";
            } else {
              record("approved");
              yield* emitStart();
              signal.throwIfAborted();
              effectAttempted = true;
              await workspace.applyEdit(proposal);
              output = `Updated ${proposal.path}. Verification has not been run.`;
              output += await postEditDiagnostics(
                [proposal.path],
                workspace.root,
                signal,
              );
            }
          }
        } else if (name === "run_command") {
          if (effectsDenied) {
            status = "denied";
            output = `${modeName} mode does not permit command execution`;
          } else {
            const args = CommandArgs.parse(call.function.arguments);
            const proposal = await createCommandProposal(
              parseArgv(args.argv),
              workspace.root,
              args.timeout_ms,
            );
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            const sandboxed = allowed === "sandbox";
            const unavailable = sandboxed
              ? (options.sandboxUnavailable ?? sandboxUnavailable)(
                  workspace.root,
                )
              : undefined;
            if (allowed !== true && !sandboxed) {
              status = "denied";
              output =
                "Command not approved. Do not repeat it unchanged: ask the user, or take another approach.";
            } else if (unavailable) {
              status = "denied";
              output = `Command not run: the command sandbox is unavailable (${unavailable}). Ask the user to approve it.`;
            } else {
              record("approved");
              yield* emitStart();
              signal.throwIfAborted();
              effectAttempted = true;
              const result = await executeApprovedCommand(
                proposal,
                signal,
                sandboxed ? {} : undefined,
              );
              uncertain =
                result.timedOut || result.cancelled || result.cleanupIncomplete;
              status =
                result.exitCode === 0 && !result.timedOut && !result.cancelled
                  ? "succeeded"
                  : "failed";
              output = JSON.stringify({
                ...result,
                output: result.output.slice(0, outputLimit),
                truncated:
                  result.truncated || result.output.length > outputLimit,
                ...(sandboxed && status === "failed"
                  ? { note: SANDBOX_NOTE }
                  : {}),
              });
            }
          }
        } else if (mcpConnection) {
          if (mcpDenied) {
            status = "denied";
            output = `${modeName} mode does not authorize MCP calls from server annotations`;
          } else {
            const argumentsJson = JSON.stringify(call.function.arguments);
            if (Buffer.byteLength(argumentsJson) > 100_000)
              throw new Error("MCP arguments exceed 100000 bytes");
            // A gateway call is approved as the tool it runs, read from the
            // very arguments that will be sent.
            const gateway = mcpConnection.gateway;
            const via =
              gateway?.call === name
                ? gatewayTarget(gateway.server, argumentsJson)
                : undefined;
            if (gateway?.call === name && !via)
              throw new Error(
                `${name} needs a server and a tool name (or SERVER__TOOL)`,
              );
            const proposal = Object.freeze({
              kind: "mcp" as const,
              name,
              argumentsJson,
              ...(via && gateway
                ? {
                    target: via.target,
                    inner: via.inner,
                    gateway: gateway.server,
                    aliases: Object.freeze(via.aliases),
                  }
                : {}),
            });
            record("approval_requested");
            const allowed = await options.approve(proposal, signal);
            signal.throwIfAborted();
            if (allowed !== true) {
              status = "denied";
              output =
                "MCP call not approved. Do not repeat it unchanged: ask the user, or take another approach.";
            } else {
              record("approved");
              yield* emitStart();
              signal.throwIfAborted();
              effectAttempted = true;
              const result = await mcpConnection.callTool(
                name,
                JSON.parse(argumentsJson),
                signal,
              );
              status = result.isError ? "failed" : "unverified";
              output = JSON.stringify({
                ...result,
                text: capOutput(result.text, outputLimit, ""),
              });
              // The server wrote this text, an error's too.
              fromServer = true;
            }
          }
        } else {
          throw new Error(`Unknown tool: ${name}`);
        }
      } catch (error) {
        uncertain = effectAttempted;
        status = "failed";
        output = signal.aborted
          ? "Cancelled; tool did not report successful completion"
          : mcpConnection && effectAttempted
            ? "MCP call did not produce a usable result; its effects are unknown"
            : error instanceof z.ZodError
              ? // One line per problem, with where it is: a small model
                // fixes "at todos[0].content" more often than it reads
                // zod's issue JSON.
                `Invalid arguments for ${name}:\n${z.prettifyError(error)}`
              : error instanceof Error
                ? error.message
                : "Tool failed";
      }
      if (uncertain) status = "outcome_unknown";
      if (status === "denied") refused.add(signature);
      // What the web or an MCP server returned is marked as such, ahead of
      // the text, so a page that reads like instructions reads as data. A
      // subagent's answer is too: it may retell pages the child fetched.
      const external =
        fromServer ||
        ((status === "succeeded" || status === "unverified") &&
          (name === "fetch" || name === "web_search" || name === "task"));
      const message: Message = {
        role: "tool",
        tool_name: name,
        tool_call_id: call.id,
        content: JSON.stringify(
          external
            ? { status, source: EXTERNAL_SOURCE, output }
            : { status, output },
        ),
      };
      if (images.length) message.images = images.splice(0);
      return { status, output, message, uncertain };
    };
    const commit = (ctx: CallContext, oc: CallOutcome) => {
      history.push(oc.message);
      if (saved && ctx.execution) {
        saved.state = saved.store.commitOutcome(
          {
            ...ctx.execution,
            type: oc.uncertain
              ? "outcome_unknown"
              : oc.status === "succeeded" || oc.status === "unverified"
                ? "completed"
                : oc.status === "denied"
                  ? "denied"
                  : signal.aborted
                    ? "cancelled"
                    : "failed",
            resultSha256: createHash("sha256")
              .update(oc.message.content)
              .digest("hex"),
            resultReceipt: oc.message.content,
          },
          saved.state.revision,
          history,
        );
      }
    };
    function* emitOutcome(
      ctx: CallContext,
      oc: CallOutcome,
    ): Generator<AgentEvent> {
      yield { type: "message", message: oc.message };
      yield {
        type: "tool_result",
        id: ctx.id,
        name: ctx.name,
        status: oc.status,
        output: oc.output,
      };
    }
    if (!parallel) {
      for (const [callIndex, call] of calls.entries()) {
        const ctx = prepare(callIndex, call);
        ctx.record("requested");
        yield { type: "tool_requested", id: ctx.id, name: ctx.name };
        const oc = yield* performCall(ctx, call, false);
        commit(ctx, oc);
        yield* emitOutcome(ctx, oc);
        if (oc.uncertain)
          throw new Error(
            "Tool outcome is uncertain; review the workspace before continuing",
          );
      }
    } else {
      // All tool_start events are emitted in call order BEFORE execution so
      // the client renders tool cards in a deterministic order; tool_result
      // events then stream as calls complete, each tagged with its call id.
      const contexts = calls.map((call, index) => prepare(index, call));
      for (const ctx of contexts) {
        ctx.record("requested");
        yield { type: "tool_requested", id: ctx.id, name: ctx.name };
        ctx.record("started");
        yield { type: "tool_start", id: ctx.id, name: ctx.name };
      }
      const outcomes: CallOutcome[] = new Array(contexts.length);
      const completedIndices: number[] = [];
      let remaining = contexts.length;
      let wake: (() => void) | undefined;
      let active = 0;
      const waiting: (() => void)[] = [];
      const acquire = () => {
        if (active < MAX_PARALLEL_TOOLS) {
          active++;
          return;
        }
        const gate = Promise.withResolvers<void>();
        waiting.push(gate.resolve);
        return gate.promise;
      };
      const release = () => {
        active--;
        waiting.shift()?.();
      };
      const drain = async <R>(
        gen: AsyncGenerator<AgentEvent, R>,
      ): Promise<R> => {
        for (;;) {
          const next = await gen.next();
          if (next.done) return next.value;
        }
      };
      const tasks = contexts.map(async (ctx, index) => {
        await acquire();
        try {
          outcomes[index] = await drain(performCall(ctx, calls[index]!, true));
        } catch (error) {
          // Error isolation: one failing call never sinks its siblings.
          const output = error instanceof Error ? error.message : "Tool failed";
          outcomes[index] = {
            status: "failed",
            output,
            message: {
              role: "tool",
              tool_name: ctx.name,
              tool_call_id: calls[index]!.id,
              content: JSON.stringify({ status: "failed", output }),
            },
            uncertain: false,
          };
        } finally {
          release();
        }
        completedIndices.push(index);
        remaining--;
        wake?.();
      });
      // A result's status goes out as its call completes; its message goes
      // out in call order, as history and the session hold them, so a
      // client that builds its history from messages keeps the same one.
      let nextMessage = 0;
      try {
        while (remaining > 0 || completedIndices.length) {
          while (!completedIndices.length && remaining > 0) {
            const gate = Promise.withResolvers<void>();
            wake = gate.resolve;
            await gate.promise;
          }
          for (const index of completedIndices.splice(0)) {
            const ctx = contexts[index]!;
            const oc = outcomes[index]!;
            yield {
              type: "tool_result",
              id: ctx.id,
              name: ctx.name,
              status: oc.status,
              output: oc.output,
            };
          }
          while (nextMessage < contexts.length && outcomes[nextMessage]) {
            yield { type: "message", message: outcomes[nextMessage]!.message };
            nextMessage++;
          }
        }
      } finally {
        wake = undefined;
        await Promise.all(tasks);
      }
      // Abort leaves partial results above; durable state lands in call
      // order so history and the revision CAS stay deterministic.
      for (const [index, ctx] of contexts.entries()) {
        const oc = outcomes[index]!;
        commit(ctx, oc);
        if (oc.uncertain)
          throw new Error(
            "Tool outcome is uncertain; review the workspace before continuing",
          );
      }
    }
    signal.throwIfAborted();
  }
  if (options.subagent) throw new SubagentStopped("turn_limit");
  throw new Error(`Agent reached the ${stepLimit}-step limit`);
}
