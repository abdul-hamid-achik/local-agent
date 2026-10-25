import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "vitest";
import {
  type AgentEvent,
  type Approval,
  type ApprovalRequest,
  batchOutputLimit,
  readLines,
  runAgent,
  toolOutputLimit,
  toolReference,
} from "#runtime/agent";
import { resolveProvider } from "#runtime/config";
import { sandboxUnavailable } from "#runtime/sandbox";
import { Workspace } from "#runtime/workspace";

type Call = { name: string; arguments: Record<string, unknown> };
type Body = {
  messages: { role: string; content: string; images?: string[] }[];
  tools: { function: { name: string; description: string } }[];
};

/** A loopback Ollama that answers each step with the next scripted tool call, then "Done". */
async function session(
  /** One call per step, or an array for a step that makes several; or the
   * steps made from the fake provider's origin. */
  steps: (Call | Call[])[] | ((origin: string) => (Call | Call[])[]),
  intent: "plan" | "build",
  approve: (proposal: ApprovalRequest) => Approval,
  options: {
    /** /api/show capabilities; the default stub is a text-only tool model. */
    capabilities?: string[];
    /** Seeds files into the workspace root before the run. */
    seed?: (root: string) => Promise<void>;
    sandboxUnavailable?: (root: string) => string | undefined;
  } = {},
) {
  const capabilities = options.capabilities ?? ["completion", "tools"];
  const root = await mkdtemp(join(tmpdir(), "local-agent-tools-"));
  await mkdir(join(root, "src"));
  await writeFile(join(root, "src", "stats.ts"), "export const mean = 1;\n");
  await options.seed?.(root);
  const bodies: Body[] = [];
  let calls: (Call | Call[])[] = [];
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
      const call =
        calls[body.messages.filter((m) => m.role === "assistant").length];
      const batch = call === undefined ? [] : [call].flat();
      return new Response(
        `${JSON.stringify({ message: { role: "assistant", content: call ? "" : "Done", ...(call ? { tool_calls: batch.map((one) => ({ function: one })) } : {}) }, done: true })}\n`,
      );
    },
  });
  calls = typeof steps === "function" ? steps(server.url.origin) : steps;
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
        ...(options.sandboxUnavailable
          ? { sandboxUnavailable: options.sandboxUnavailable }
          : {}),
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
      "task",
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
      "task",
      "todo_write",
    ]);
    expect(run.results.map((result) => result.status)).toEqual(["denied"]);
    expect(run.proposals).toEqual([]);
    expect(await Bun.file(join(run.root, "a.txt")).exists()).toBe(false);
  } finally {
    await run.cleanup();
  }
});

test("the tool reference is what runs offer: build every tool, plan the read-only ones", async () => {
  const reference = toolReference({ ask: false, goal: false });
  const build = await session([], "build", () => true);
  const plan = await session([], "plan", () => true);
  try {
    const offered = (run: typeof build) => run.bodies[0]?.tools ?? [];
    expect(reference.map((tool) => tool.name)).toEqual(
      offered(build).map((tool) => tool.function.name),
    );
    expect(
      reference.filter((tool) => tool.readOnly).map((tool) => tool.name),
    ).toEqual(offered(plan).map((tool) => tool.function.name));
  } finally {
    await build.cleanup();
    await plan.cleanup();
  }
});

test("every builtin has a one-line summary for the reference", () => {
  const reference = toolReference({ ask: true, goal: true });
  expect(reference).toHaveLength(13);
  for (const tool of reference)
    expect(tool.summary, tool.name).toMatch(/^[A-Z][^\n]{9,99}\.$/u);
});

test("the tool reference offers ask_user and goal_status only where the host does", () => {
  const names = (ask: boolean, goal: boolean) =>
    toolReference({ ask, goal }).map((tool) => tool.name);
  expect(names(false, false)).not.toContain("ask_user");
  expect(names(false, false)).not.toContain("goal_status");
  expect(names(true, false)).toContain("ask_user");
  expect(names(true, false)).not.toContain("goal_status");
  expect(names(false, true)).toContain("goal_status");
  expect(
    toolReference({ ask: true, goal: true }).filter((tool) =>
      ["ask_user", "goal_status"].includes(tool.name),
    ),
  ).toEqual([
    expect.objectContaining({ name: "ask_user", readOnly: true }),
    expect.objectContaining({ name: "goal_status", readOnly: true }),
  ]);
});

test("build asks before every tool the reference says has an effect", async () => {
  // A new tool with an effect needs a sample call here.
  const samples: Record<string, Record<string, unknown>> = {
    edit: { path: "src/stats.ts", old_string: "1", new_string: "2" },
    write: { path: "a.txt", content: "x" },
    run_command: { argv: ["true"] },
  };
  const effects = toolReference({ ask: false, goal: false })
    .filter((tool) => !tool.readOnly)
    .map((tool) => tool.name);
  expect(effects).toEqual(Object.keys(samples));
  const run = await session(
    effects.map((name) => ({ name, arguments: samples[name] ?? {} })),
    "build",
    () => false,
  );
  try {
    expect(run.proposals).toHaveLength(effects.length);
    expect(run.results.map((result) => result.status)).toEqual(
      effects.map(() => "denied"),
    );
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

test("a repeated read_image is a note only when the image is the same", async () => {
  const shot = { name: "read_image", arguments: { path: "shot.png" } };
  // Same size, other bytes: the text is the same, the image is not.
  const append = {
    name: "run_command",
    arguments: {
      argv: [
        process.execPath,
        "-e",
        'require("node:fs").appendFileSync("shot.png", "!")',
      ],
    },
  };
  const run = await session([shot, append, shot, shot], "build", () => true, {
    capabilities: ["completion", "tools", "vision"],
    seed: (root) => writeFile(join(root, "shot.png"), fakePng(3, 2)),
  });
  try {
    const sent = (step: number) => run.bodies[step]?.messages.at(-1);
    expect(run.results.map((result) => result.status)).toEqual([
      "succeeded",
      "succeeded",
      "succeeded",
      "succeeded",
    ]);
    expect(run.results[2]?.output).toBe(
      "image 3x2 PNG from shot.png (attached)",
    );
    expect(sent(3)?.images).toHaveLength(1);
    expect(sent(3)?.images).not.toEqual(sent(1)?.images);
    expect(run.results[3]?.output).toContain("returned the same result");
    expect(sent(4)?.images).toBeUndefined();
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

test("one tool result holds a quarter of the context window", () => {
  expect(toolOutputLimit(16_384)).toBe(12_288);
  expect(toolOutputLimit(32_768)).toBe(24_576);
  expect(toolOutputLimit(2_048)).toBe(4_000);
  expect(toolOutputLimit(262_144)).toBe(100_000);
});

test("the results of one message share 40% of the window", () => {
  expect(batchOutputLimit(32_768, 0)).toBe(24_576);
  expect(batchOutputLimit(32_768, 1)).toBe(24_576);
  expect(batchOutputLimit(32_768, 2)).toBe(19_660);
  expect(batchOutputLimit(32_768, 4)).toBe(9_830);
  expect(batchOutputLimit(32_768, 32)).toBe(1_228);
  expect(batchOutputLimit(2_048, 32)).toBe(200);
  // Even at 32 calls the batch stays inside 40% of the window.
  expect(batchOutputLimit(8_192, 32) * 32).toBeLessThanOrEqual(8_192 * 1.2);
});

test("a batch of reads is cut to its share, and the next step to its own", async () => {
  const big = { name: "read", arguments: { path: "big.txt" } };
  const run = await session([[big, big, big, big], big], "plan", () => true, {
    seed: (root) =>
      writeFile(
        join(root, "big.txt"),
        Array.from({ length: 5_000 }, (_, i) => `row ${i + 1}`).join("\n"),
      ),
  });
  try {
    const sizes = run.results.map((result) => result.output.length);
    expect(sizes).toHaveLength(5);
    // The default 16384-token window: 4 × 4915, then one whole 12288.
    for (const size of sizes.slice(0, 4)) {
      expect(size).toBeLessThanOrEqual(4_915);
      expect(size).toBeGreaterThan(4_000);
    }
    expect(sizes[4]).toBeGreaterThan(12_000);
    expect(sizes[4]).toBeLessThanOrEqual(12_288);
  } finally {
    await run.cleanup();
  }
});

test("the same inspection call again in a turn, with the same result, is answered with a note", async () => {
  const read = { name: "read", arguments: { path: "src/stats.ts" } };
  const list = { name: "list", arguments: { path: "src" } };
  const edit = {
    name: "edit",
    arguments: {
      path: "src/stats.ts",
      old_string: "mean = 1",
      new_string: "mean = 2",
    },
  };
  const refused = {
    name: "edit",
    arguments: {
      ...edit.arguments,
      old_string: "mean = 2",
      new_string: "mean = 3",
    },
  };
  const run = await session(
    [read, list, read, edit, read, read, refused, read],
    "build",
    (proposal) =>
      !(proposal.kind === "edit" && proposal.after.includes("mean = 3")),
  );
  try {
    const NOTE =
      "This exact read call returned the same result earlier in this turn, and that result is above: use it, or try something else.";
    expect(run.results.map((result) => [result.name, result.status])).toEqual([
      ["read", "succeeded"],
      ["list", "succeeded"],
      ["read", "succeeded"],
      ["edit", "succeeded"],
      ["read", "succeeded"],
      ["read", "succeeded"],
      ["edit", "denied"],
      ["read", "succeeded"],
    ]);
    const outputs = run.results.map((result) => result.output);
    // An edit that ran changes the result; a refused one does not.
    expect(outputs[0]).toBe("export const mean = 1;\n");
    expect(outputs[2]).toBe(NOTE);
    expect(outputs[4]).toBe("export const mean = 2;\n");
    expect(outputs[5]).toBe(NOTE);
    expect(outputs[7]).toBe(NOTE);
  } finally {
    await run.cleanup();
  }
});

test("read pages through a file that does not fit, at line ends", () => {
  const file = Array.from({ length: 100 }, (_, i) => `line ${i + 1}`).join(
    "\n",
  );
  // Whole and unannotated when it fits and no range is asked for.
  expect(readLines(`${file}\n`, undefined, undefined, 4_000)).toBe(`${file}\n`);
  expect(readLines(file, 10, 3, 4_000)).toBe(
    "line 10\nline 11\nline 12\n[lines 10-12 of 100; read again with offset 13 to continue]",
  );
  expect(readLines(file, 99, undefined, 4_000)).toBe(
    "line 99\nline 100\n[lines 99-100 of 100]",
  );
  expect(readLines(file, 101, undefined, 4_000)).toBe(
    "[the file has 100 lines; offset 101 is past its end]",
  );
  expect(readLines(file, 500, undefined, 4_000)).toBe(
    "[the file has 100 lines; offset 500 is past its end]",
  );
  // Too long for the limit: the lines that fit, and where to go on.
  const part = readLines(file, undefined, undefined, 300);
  expect(part.length).toBeLessThanOrEqual(300);
  expect(part).toMatch(
    /\n\[lines 1-(\d+) of 100; read again with offset (\d+) to continue\]$/,
  );
  const [, last, next] =
    /lines 1-(\d+) of 100; read again with offset (\d+)/.exec(part) ?? [];
  expect(Number(next)).toBe(Number(last) + 1);
  expect(part.split("\n").at(-2)).toBe(`line ${last}`);
  // One line longer than the limit is cut, not returned whole, and the
  // note says so (no offset reaches the rest of it).
  const long = readLines(
    `${"x".repeat(10_000)}\nnext`,
    undefined,
    undefined,
    4_000,
  );
  expect(long.length).toBeLessThanOrEqual(4_000);
  expect(long).toMatch(
    /\n\[lines 1-1 of 2; line 1 is cut at 3800 of 10000 characters \(search can find text in the rest\); read again with offset 2 to continue\]$/,
  );
});

test("read takes offset and limit, as numbers or strings", async () => {
  const run = await session(
    [
      {
        name: "read",
        arguments: { path: "big.txt", offset: "3", limit: 2 },
      },
      { name: "read", arguments: { path: "big.txt", offset: null } },
    ],
    "plan",
    () => true,
    {
      seed: (root) =>
        writeFile(
          join(root, "big.txt"),
          Array.from({ length: 5_000 }, (_, i) => `row ${i + 1}`).join("\n"),
        ),
    },
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual([
      "succeeded",
      "succeeded",
    ]);
    expect(run.results[0]?.output).toBe(
      "row 3\nrow 4\n[lines 3-4 of 5000; read again with offset 5 to continue]",
    );
    // The default 16384-token context: 12288 characters, then a pointer.
    const whole = run.results[1]?.output ?? "";
    expect(whole.length).toBeLessThanOrEqual(12_288);
    expect(whole).toMatch(/read again with offset \d+ to continue\]$/);
  } finally {
    await run.cleanup();
  }
});

test("a refused call repeated in the same turn is not asked again", async () => {
  const edit = {
    name: "edit",
    arguments: {
      path: "src/stats.ts",
      old_string: "mean = 1",
      new_string: "mean = 2",
    },
  };
  // The same arguments in another key order are the same call.
  const reordered = {
    name: "edit",
    arguments: {
      new_string: "mean = 2",
      old_string: "mean = 1",
      path: "src/stats.ts",
    },
  };
  const run = await session([edit, edit, reordered], "build", () => false);
  try {
    expect(run.proposals).toHaveLength(1);
    expect(run.results.map((result) => result.status)).toEqual([
      "denied",
      "denied",
      "denied",
    ]);
    expect(run.results[2]?.output).toMatch(
      /^This exact edit call was already refused in this turn/,
    );
    expect(run.results[0]?.output).toBe(
      "Edit not approved. Do not repeat it unchanged: ask the user, or take another approach.",
    );
    expect(run.results[1]?.output).toMatch(
      /^This exact edit call was already refused in this turn/,
    );
  } finally {
    await run.cleanup();
  }
});

const bunCall = (script: string) => ({
  name: "run_command",
  arguments: { argv: [process.execPath, "-e", script] },
});

test.runIf(sandboxUnavailable(tmpdir()) === undefined)(
  "a sandbox approval runs a command confined and refuses any other effect",
  async () => {
    // Outside the workspace (its parent): writable only unconfined.
    const outside = join(
      tmpdir(),
      `local-agent-outside-${process.pid}-${Date.now()}`,
    );
    const fetchTags = (origin: string, label: string) =>
      bunCall(
        `/* ${label} */ fetch(${JSON.stringify(`${origin}/api/tags`)}).then(() => process.exit(0), () => process.exit(3))`,
      );
    const run = await session(
      (origin) => [
        { name: "write", arguments: { path: "w.txt", content: "x" } },
        bunCall(`require("node:fs").writeFileSync("in.txt", "x")`),
        bunCall(
          `require("node:fs").writeFileSync(${JSON.stringify(outside)}, "x")`,
        ),
        // No network at all: not even the provider's own port, which answers
        // the same command run outside.
        fetchTags(origin, "confined"),
        fetchTags(origin, "outside"),
      ],
      "build",
      (proposal) =>
        proposal.kind === "command" &&
        proposal.argv.join(" ").includes("outside */")
          ? true
          : "sandbox",
    );
    try {
      expect(run.results[0]?.status).toBe("denied");
      expect(await Bun.file(join(run.root, "w.txt")).exists()).toBe(false);
      expect(run.results.slice(1).map((result) => result.status)).toEqual([
        "succeeded",
        "failed",
        "failed",
        "succeeded",
      ]);
      const [ran, wrote, fetched, control] = run.results.slice(1).map(
        (result) =>
          JSON.parse(result.output) as {
            sandboxed?: boolean;
            exitCode: number;
            note?: string;
          },
      );
      expect(ran).toMatchObject({ sandboxed: true, exitCode: 0 });
      expect(ran?.note).toBeUndefined();
      expect(await Bun.file(join(run.root, "in.txt")).exists()).toBe(true);
      expect(wrote?.sandboxed).toBe(true);
      expect(wrote?.note).toContain("ran in the command sandbox");
      expect(await Bun.file(outside).exists()).toBe(false);
      expect(fetched).toMatchObject({ sandboxed: true, exitCode: 3 });
      expect(control?.exitCode).toBe(0);
      expect(control?.sandboxed).toBeUndefined();
    } finally {
      await rm(outside, { force: true });
      await run.cleanup();
    }
  },
);

test("a sandbox approval where the sandbox is unavailable runs nothing", async () => {
  const run = await session(
    [bunCall(`require("node:fs").writeFileSync("in.txt", "x")`)],
    "build",
    () => "sandbox",
    { sandboxUnavailable: () => "the command sandbox needs macOS (Seatbelt)" },
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual(["denied"]);
    expect(run.results[0]?.output).toBe(
      "Command not run: the command sandbox is unavailable (the command sandbox needs macOS (Seatbelt)). Ask the user to approve it.",
    );
    expect(await Bun.file(join(run.root, "in.txt")).exists()).toBe(false);
  } finally {
    await run.cleanup();
  }
});

test("a command stopped by its timeout reports partial changes, and the turn goes on", async () => {
  const run = await session(
    [
      {
        name: "run_command",
        arguments: {
          argv: [process.execPath, "-e", "setTimeout(() => {}, 60_000)"],
          timeout_ms: 300,
        },
      },
    ],
    "build",
    () => true,
  );
  try {
    expect(run.results.map((result) => result.status)).toEqual(["failed"]);
    const result = JSON.parse(run.results[0]?.output ?? "{}") as {
      timedOut: boolean;
      cleanupIncomplete: boolean;
      note?: string;
    };
    expect(result).toMatchObject({ timedOut: true, cleanupIncomplete: false });
    expect(result.note).toBe(
      "The command was stopped before it finished and may have made partial changes or left a background process running: check before running it again.",
    );
    // The model was asked again, with the result: no uncertain stop.
    expect(run.bodies).toHaveLength(2);
  } finally {
    await run.cleanup();
  }
});

/** Waits, 10 s at most, for the process whose pid the file names to exit. */
async function childGone(file: string): Promise<void> {
  for (let tries = 0; tries < 200; tries++) {
    const pid = Number(await readFile(file, "utf8").catch(() => ""));
    if (pid > 0) {
      try {
        process.kill(pid, 0);
      } catch (error) {
        // EPERM: a process is there, only not ours to signal.
        if ((error as { code?: unknown }).code === "ESRCH") return;
      }
    }
    await Bun.sleep(50);
  }
  throw new Error(`the child that wrote ${file} is still running`);
}

test("a command that leaves a process holding its output has an unknown outcome", async () => {
  // The child leaves the process group (setsid) and keeps stdout open, so
  // the runner cannot see the command end: the turn stops there. It holds
  // on until the test is done (a stop file, 30 s at most), not for a fixed
  // time a loaded machine may outlast before the runner sees the leader go.
  // It writes its pid, so the test can wait for it to be gone.
  const script =
    'use POSIX; if (fork) { exit 0 } POSIX::setsid(); open(my $f, ">", "pid.tmp"); print $f $$; close $f; rename "pid.tmp", "pid"; for (1..300) { last if -e "stop"; select(undef, undef, undef, 0.1) } exit 0';
  let root = "";
  try {
    await expect(
      session(
        [{ name: "run_command", arguments: { argv: ["perl", "-e", script] } }],
        "build",
        () => true,
        {
          seed: async (dir) => {
            root = dir;
          },
        },
      ),
    ).rejects.toThrow("Tool outcome is uncertain");
  } finally {
    if (root) {
      await writeFile(join(root, "stop"), "");
      await childGone(join(root, "pid"));
      await rm(root, { recursive: true, force: true });
    }
  }
});
