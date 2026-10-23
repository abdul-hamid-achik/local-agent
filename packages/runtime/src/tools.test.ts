import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  type AgentEvent,
  type ApprovalRequest,
  runAgent,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Body = {
  messages: { role: string; content: string; images?: string[] }[];
  tools: { function: { name: string; description: string } }[];
};

/** A loopback Ollama that answers each step with the next scripted tool call, then "Done". */
async function session(
  calls: Call[],
  intent: "plan" | "build",
  approve: (proposal: ApprovalRequest) => boolean,
  options: {
    /** /api/show capabilities; the default stub is a text-only tool model. */
    capabilities?: string[];
    /** Seeds files into the workspace root before the run. */
    seed?: (root: string) => Promise<void>;
  } = {},
) {
  const capabilities = options.capabilities ?? ["completion", "tools"];
  const root = await mkdtemp(join(tmpdir(), "local-agent-tools-"));
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
  await options.seed?.(root);
  const bodies: Body[] = [];
  const server = Bun.serve({
    hostname: "127.0.0.1",
    port: 0,
    async fetch(request) {
      const path = new URL(request.url).pathname;
      if (path === "/api/tags")
        return Response.json({ models: [{ name: "fixture", size: 1000 }] });
      if (path === "/api/show")
        return Response.json({
          capabilities,
          model_info: { "fixture.context_length": 16384 },
        });
      const body = (await request.json()) as Body;
      bodies.push(body);
      const call = calls[body.messages.filter((m) => m.role === "tool").length];
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Done", ...(call ? { tool_calls: [{ function: call }] } : {}) }, done: true })}\n`,
      );
    },
  });
  const proposals: ApprovalRequest[] = [];
  const events: AgentEvent[] = [];
  try {
    using workspace = await Workspace.create(root);
    for await (const event of runAgent(
      resolveProvider({ OLLAMA_HOST: server.url.origin }, "fixture"),
      [{ role: "user", content: "go" }],
      {
        workspace,
        intent,
        signal: AbortSignal.timeout(10_000),
        approve: async (proposal) => {
          proposals.push(proposal);
          return approve(proposal);
        },
      },
    ))
      events.push(event);
  } finally {
    await server.stop(true);
  }
  const results = events.flatMap((event) =>
    event.type === "tool_result" ? [event] : [],
  );
  return {
    root,
    bodies,
    proposals,
    results,
    cleanup: () => rm(root, { recursive: true, force: true }),
  };
}

test("build intent offers every tool with a relative-path example; the prompt states the rules", async () => {
  const run = await session([], "build", () => true);
  try {
    const body = run.bodies[0] as Body;
    expect(body.tools.map((tool) => tool.function.name)).toEqual([
      "read",
      "read_image",
      "list",
      "search",
      "fetch",
      "web_search",
      "edit",
      "write",
      "run_command",
      "todo_write",
    ]);
    for (const name of ["read", "edit", "write"])
      expect(
        body.tools.find((tool) => tool.function.name === name)?.function
          .description,
      ).toContain("src/stats.ts");
    const command = body.tools.find(
      (tool) => tool.function.name === "run_command",
    );
    expect(command?.function.description).toContain('"argv": ["bun", "test"');
    expect(command?.function.description).toContain("There is no shell");
    expect(body.messages[0]?.content).toContain(
      "relative to the workspace root",
    );
  } finally {
    await run.cleanup();
  }
});

test("plan intent offers the read-only tools and denies writes", async () => {
  const run = await session(
    [{ name: "write", arguments: { path: "a.txt", content: "x" } }],
    "plan",
    () => true,
  );
  try {
    expect(run.bodies[0]?.tools.map((tool) => tool.function.name)).toEqual([
      "read",
      "read_image",
      "list",
      "search",
      "fetch",
      "web_search",
      "todo_write",
    ]);
    expect(run.results.map((result) => result.status)).toEqual(["denied"]);
    expect(run.proposals).toEqual([]);
    expect(await Bun.file(join(run.root, "a.txt")).exists()).toBe(false);
  } finally {
    await run.cleanup();
  }
});

test("list and search need no approval; write and run_command do, and show what is approved", async () => {
  const run = await session(
    [
      { name: "list", arguments: {} },
      { name: "list", arguments: { path: "src" } },
      {
        name: "search",
        arguments: { pattern: "mean", regex: "false", recursive: true },
      },
      { name: "read", arguments: { path: "/src/stats.ts" } },
      {
        name: "write",
        arguments: { path: "src/new.ts", content: "export {};\n" },
      },
      {
        name: "run_command",
        arguments: { argv: 'bun test "my tests/a b.ts"' },
      },
      { name: "run_command", arguments: { argv: "find . | head" } },
    ],
    "build",
    (proposal) => proposal.kind === "write",
  );
  try {
    expect(run.results.map((result) => [result.name, result.status])).toEqual([
      ["list", "succeeded"],
      ["list", "succeeded"],
      ["search", "succeeded"],
      ["read", "failed"],
      ["write", "succeeded"],
      ["run_command", "denied"],
      ["run_command", "failed"],
    ]);
    expect(run.results[0]?.output).toBe("src/");
    expect(run.results[1]?.output).toBe("stats.ts");
    expect(run.results[2]?.output).toBe(
      "src/stats.ts:1: export const mean = 1;",
    );
    expect(run.results[3]?.output).toContain('Use "src/stats.ts"');
    expect(run.results[4]?.output).toContain("Created src/new.ts");
    expect(await readFile(join(run.root, "src", "new.ts"), "utf8")).toBe(
      "export {};\n",
    );
    // Only the write and the commands asked; the command shows its parsed argv.
    expect(run.proposals.map((proposal) => proposal.kind)).toEqual([
      "write",
      "command",
    ]);
    expect(run.proposals[0]).toMatchObject({
      created: true,
      path: "src/new.ts",
    });
    expect(run.proposals[1]).toMatchObject({
      argv: ["bun", "test", "my tests/a b.ts"],
    });
    // The shell string was refused before any approval, with the reason.
    expect(run.results[6]?.output).toContain("no shell");
  } finally {
    await run.cleanup();
  }
});

test("an absolute path inside the workspace is accepted and edited by its relative form", async () => {
  const root = await mkdtemp(join(tmpdir(), "local-agent-abs-"));
  try {
    await mkdir(join(root, "src"));
    await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
    using workspace = await Workspace.create(root);
    const absolute = join(workspace.root, "src", "stats.ts");
    expect(await workspace.read(absolute)).toContain("mean");
    expect((await workspace.prepareEdit(absolute, "1", "2")).path).toBe(
      "src/stats.ts",
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

const fakePng = (width: number, height: number) => {
  const bytes = Buffer.alloc(24);
  bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0);
  bytes.write("IHDR", 12, "latin1");
  bytes.writeUInt32BE(width, 16);
  bytes.writeUInt32BE(height, 20);
  return bytes;
};

test("read_image attaches the base64 image to its own tool result message", async () => {
  const png = fakePng(3, 2);
  const run = await session(
    [{ name: "read_image", arguments: { path: "shot.png" } }],
    "build",
    () => true,
    {
      capabilities: ["completion", "tools", "vision"],
      seed: (root) => writeFile(join(root, "shot.png"), png),
    },
  );
  try {
    expect(run.results.map((result) => [result.name, result.status])).toEqual([
      ["read_image", "succeeded"],
    ]);
    expect(run.results[0]?.output).toBe(
      "image 3x2 PNG from shot.png (attached)",
    );
    // The image rides the tool message of the next request; the text stays short.
    const toolMessage = run.bodies[1]?.messages.at(-1);
    expect(toolMessage?.role).toBe("tool");
    expect(toolMessage?.images).toEqual([png.toString("base64")]);
  } finally {
    await run.cleanup();
  }
});

test("read_image checks content, not extensions: real images pass, text fails", async () => {
  const run = await session(
    [
      { name: "read_image", arguments: { path: "chart.png" } },
      { name: "read_image", arguments: { path: "icon.gif" } },
      { name: "read_image", arguments: { path: "photo.jpg" } },
      { name: "read_image", arguments: { path: "shot.webp" } },
      { name: "read_image", arguments: { path: "fake.png" } },
      { name: "read_image", arguments: { path: "notes.txt" } },
    ],
    "build",
    () => true,
    {
      capabilities: ["completion", "tools", "vision"],
      seed: async (root) => {
        const gif = Buffer.alloc(13);
        gif.write("GIF89a", 0, "latin1");
        gif.writeUInt16LE(4, 6);
        gif.writeUInt16LE(2, 8);
        const jpeg = Buffer.alloc(20);
        jpeg.set([0xff, 0xd8, 0xff, 0xc0], 0);
        jpeg.writeUInt16BE(17, 4);
        jpeg[6] = 8;
        jpeg.writeUInt16BE(5, 7);
        jpeg.writeUInt16BE(7, 9);
        const webp = Buffer.alloc(30);
        webp.write("RIFF", 0, "latin1");
        webp.writeUInt32LE(18, 4);
        webp.write("WEBP", 8, "latin1");
        webp.write("VP8X", 12, "latin1");
        webp[24] = 6;
        webp[27] = 1;
        await writeFile(join(root, "chart.png"), fakePng(3, 2));
        await writeFile(join(root, "icon.gif"), gif);
        await writeFile(join(root, "photo.jpg"), jpeg);
        await writeFile(join(root, "shot.webp"), webp);
        await writeFile(join(root, "fake.png"), "definitely not an image");
        await writeFile(join(root, "notes.txt"), "just text");
      },
    },
  );
  try {
    expect(run.results.map((result) => [result.name, result.status])).toEqual([
      ["read_image", "succeeded"],
      ["read_image", "succeeded"],
      ["read_image", "succeeded"],
      ["read_image", "succeeded"],
      ["read_image", "failed"],
      ["read_image", "failed"],
    ]);
    expect(run.results[0]?.output).toBe(
      "image 3x2 PNG from chart.png (attached)",
    );
    expect(run.results[1]?.output).toBe(
      "image 4x2 GIF from icon.gif (attached)",
    );
    expect(run.results[2]?.output).toBe(
      "image 7x5 JPEG from photo.jpg (attached)",
    );
    expect(run.results[3]?.output).toBe(
      "image 7x2 WebP from shot.webp (attached)",
    );
    expect(run.results[4]?.output).toMatch(
      /not a PNG, JPEG, WebP or GIF image.*checked, not its extension/s,
    );
    expect(run.results[5]?.output).toMatch(
      /not a PNG, JPEG, WebP or GIF image.*checked, not its extension/s,
    );
  } finally {
    await run.cleanup();
  }
});

test("read_image refuses images over 5 MiB and paths outside the workspace", async () => {
  const oversize = Buffer.alloc(5 * 1024 * 1024 + 1);
  oversize.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0);
  const run = await session(
    [
      { name: "read_image", arguments: { path: "big.png" } },
      { name: "read_image", arguments: { path: "../outside.png" } },
      { name: "read_image", arguments: { path: "/etc/passwd" } },
    ],
    "build",
    () => true,
    {
      capabilities: ["completion", "tools", "vision"],
      seed: (root) => writeFile(join(root, "big.png"), oversize),
    },
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual([
      "failed",
      "failed",
      "failed",
    ]);
    expect(run.results[0]?.output).toMatch(/exceeds size limit/);
    expect(run.results[1]?.output).toMatch(/escapes root/);
    expect(run.results[2]?.output).toMatch(/outside it|must be relative/);
  } finally {
    await run.cleanup();
  }
});

test("read_image fails fast with the vision requirement on a text-only model", async () => {
  const run = await session(
    [{ name: "read_image", arguments: { path: "shot.png" } }],
    "build",
    () => true,
    { seed: (root) => writeFile(join(root, "shot.png"), fakePng(3, 2)) },
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual(["failed"]);
    expect(run.results[0]?.output).toBe(
      "read_image: model fixture has no vision capability; switch to a multimodal model such as llava, llama3.2-vision, qwen2.5vl or minicpm-v",
    );
    // Nothing was attached when the model cannot see it.
    expect(run.bodies[1]?.messages.at(-1)?.images).toBeUndefined();
  } finally {
    await run.cleanup();
  }
});
