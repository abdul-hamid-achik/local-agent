// The Tuimark loop for ui/app.tui as one reproducible command:
//
//   bun run tuimark:check            verify
//   bun run tuimark:check --update   regenerate samples and goldens, then verify
//
// It (1) rebuilds the sample stores from the code that builds the real store,
// (2) runs `tuimark validate --strict` on each, (3) compares `tuimark dump` text
// at 80 and 120 columns with the goldens in ui/golden/, and (4) runs
// `tuimark play` to prove that every action of the document fires from the
// keys the hints promise. The binary is TUIMARK_BINARY, else the SDK's own
// resolution (cache, then a checked download of the pinned release).

import { mkdir } from "node:fs/promises";
import { dirname, join } from "node:path";
import { resolveBinary } from "@thelacanians/tuimark";
import { SAMPLES } from "#cli/tuimark/fixtures";

const ui = join(dirname(import.meta.path), "ui");
const update = Bun.argv.includes("--update");
const rows = 30;
const widths = [80, 120];
const failures: string[] = [];

const bin = await resolveBinary();

function tuimark(args: string[]): { code: number; out: string; err: string } {
  const run = Bun.spawnSync([bin, ...args], { cwd: ui });
  return {
    code: run.exitCode,
    out: run.stdout.toString(),
    err: run.stderr.toString(),
  };
}

async function matches(path: string, expected: string, what: string) {
  const file = Bun.file(path);
  if (update) {
    await mkdir(dirname(path), { recursive: true });
    await Bun.write(path, expected);
    return;
  }
  if (!(await file.exists()) || (await file.text()) !== expected)
    failures.push(
      `${what} is stale: ${path} (run bun run tuimark:check --update)`,
    );
}

console.log(`tuimark: ${tuimark(["version"]).out.trim()} (${bin})`);

// 1 + 2 + 3: samples, validate, goldens.
for (const [name, make] of Object.entries(SAMPLES)) {
  const data = `samples/${name}.json`;
  await matches(
    join(ui, data),
    `${JSON.stringify(make(), null, 2)}\n`,
    "sample",
  );
  const valid = tuimark(["validate", "app.tui", "--data", data, "--strict"]);
  if (valid.code !== 0 || valid.out.trim() !== "ok")
    failures.push(`validate ${name}: ${valid.out}${valid.err}`.trim());
  for (const cols of widths) {
    const args = [
      "dump",
      "app.tui",
      "--data",
      data,
      "--cols",
      String(cols),
      "--rows",
      String(rows),
    ];
    const json = JSON.parse(tuimark([...args, "--format", "json"]).out) as {
      errors: unknown[];
    };
    if (json.errors.length > 0)
      failures.push(`dump ${name} at ${cols}: ${JSON.stringify(json.errors)}`);
    const text =
      tuimark([...args, "--format", "text"]).out.split("=== nodes ===")[0] ??
      "";
    await matches(join(ui, "golden", `${name}-${cols}.txt`), text, "golden");
  }
}

// 4: every action fires, from the sample where its key is live.
interface Expect {
  sample: string;
  input: string;
  action: string;
  value?: unknown;
}
const plays: Expect[] = [
  {
    sample: "sample",
    input: "text:hello enter",
    action: "send",
    value: "hello",
  },
  {
    sample: "sample",
    input: "text:/help enter",
    action: "send",
    value: "/help",
  },
  { sample: "sample", input: "esc", action: "cancel" },
  { sample: "sample", input: "ctrl+c", action: "quit" },
  { sample: "sample", input: "ctrl+k", action: "palette_open" },
  { sample: "sample-approval", input: "y", action: "dialog_confirm" },
  { sample: "sample-approval", input: "n", action: "dialog_cancel" },
  { sample: "sample-approval", input: "esc", action: "dialog_cancel" },
  { sample: "sample-approval", input: "enter", action: "dialog_cancel" },
  { sample: "sample-approval", input: "ctrl+c", action: "quit" },
  {
    sample: "sample-streaming",
    input: "focus:#earlier enter",
    action: "load_earlier",
  },
  { sample: "sample-streaming", input: "ctrl+o", action: "load_earlier" },
  {
    sample: "sample-palette",
    input: "text:x",
    action: "palette_query",
    value: "modex",
  },
  {
    sample: "sample-palette",
    input: "enter",
    action: "palette_run",
    value: "mode",
  },
  { sample: "sample-palette", input: "esc", action: "palette_close" },
  { sample: "sample-help", input: "q", action: "help_close" },
  { sample: "sample-help", input: "esc", action: "help_close" },
];
for (const check of plays) {
  const run = tuimark([
    "play",
    "app.tui",
    "--data",
    `samples/${check.sample}.json`,
    "--input",
    check.input,
    "--format",
    "json",
  ]);
  const label = `play ${check.sample} "${check.input}"`;
  try {
    const events = (
      JSON.parse(run.out) as { events: { action: string; value: unknown }[] }
    ).events;
    const event = events.find((e) => e.action === check.action);
    if (!event)
      failures.push(
        `${label}: expected ${check.action}, got ${events.map((e) => e.action).join(",") || "nothing"}`,
      );
    else if ("value" in check && event.value !== check.value)
      failures.push(
        `${label}: ${check.action} value ${JSON.stringify(event.value)}, expected ${JSON.stringify(check.value)}`,
      );
  } catch {
    failures.push(`${label}: ${run.err || run.out}`.trim());
  }
}

// Focus lands on Deny when the approval opens (the safe button).
const approval = JSON.parse(
  tuimark([
    "dump",
    "app.tui",
    "--data",
    "samples/sample-approval.json",
    "--cols",
    "80",
    "--rows",
    String(rows),
    "--format",
    "json",
  ]).out,
) as { focus: string };
if (approval.focus !== "approval__no")
  failures.push(`approval focus is ${approval.focus}, expected approval__no`);

if (failures.length > 0) {
  console.error(failures.map((f) => `FAIL ${f}`).join("\n"));
  process.exit(1);
}
console.log(
  `ok: ${Object.keys(SAMPLES).length} samples validated, ${Object.keys(SAMPLES).length * widths.length} goldens, ${plays.length} play checks${update ? " (updated)" : ""}`,
);
