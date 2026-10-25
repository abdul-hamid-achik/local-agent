// Builds the release archive of this machine's platform:
//
//   bun scripts/release-binary.ts [--out dist/release]
//
// writes <out>/local-agent_<version>_<os>_<arch>.tar.gz. The archive is flat,
// as the Go releases' were, so the cask's `binary "local-agent"` and the
// installer find the same paths on every platform: the compiled
// `local-agent` executable, the Tuimark client's ui/ next to it (the host is
// another process and reads its documents from disk, so they cannot live
// inside the executable), the LICENSE and the README. Only the host
// platform is built: the Vue client loads a native library, so a
// release is compiled on each platform's own runner (release.yml).
//
// The executable never reads the working directory's bunfig.toml or .env.
// The working directory is the workspace the agent edits, often a repository
// nobody has vetted: a bunfig `preload` would run its code before ours, and a
// .env could set LOCAL_AGENT_LOCAL_ONLY=false and a remote provider. The
// source launcher's shebang turns both off; these flags do the same for the
// compiled one, and scripts/release-smoke.ts proves it on the archive.

import { cp, mkdir, rm } from "node:fs/promises";
import { basename, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const cli = join(root, "apps/cli");

/** The archive's platform words, as the Go releases and the cask name them. */
export function platform(
  os: string = process.platform,
  arch: string = process.arch,
): string {
  const osName = { darwin: "darwin", linux: "linux" }[os];
  const archName = { arm64: "arm64", x64: "amd64" }[arch];
  if (!osName || !archName)
    throw new Error(`no release build for ${os}/${arch}`);
  return `${osName}_${archName}`;
}

/** The ui/ files a release ships: documents, sheets, kit and themes. */
export function shipped(path: string): boolean {
  const name = basename(path);
  if (name.startsWith(".")) return false;
  // Test material: the text goldens and the sample stores check.ts uses.
  return !/(^|\/)(golden|samples)(\/|$)/.test(path);
}

if (import.meta.main) {
  const { values } = parseArgs({
    args: Bun.argv.slice(2),
    options: { out: { type: "string", default: "dist/release" } },
  });
  const { version } = await Bun.file(join(cli, "package.json")).json();
  const name = `local-agent_${version}_${platform()}`;
  const out = join(root, values.out ?? "dist/release");
  const stage = join(out, name);
  await rm(stage, { recursive: true, force: true });
  await mkdir(stage, { recursive: true });

  const build = Bun.spawnSync(
    [
      process.execPath,
      "build",
      "--compile",
      "--no-compile-autoload-dotenv",
      "--no-compile-autoload-bunfig",
      "src/main.ts",
      "--outfile",
      join(stage, "local-agent"),
    ],
    { cwd: cli, stdout: "inherit", stderr: "inherit" },
  );
  if (build.exitCode !== 0) throw new Error("bun build --compile failed");

  // Appending the program to Bun's executable breaks Bun's own signature.
  // The arm64 linker signs its output ad hoc; an x86-64 build keeps the
  // broken one, which `codesign --verify` refuses. Signed ad hoc here, both
  // carry the same valid signature.
  if (process.platform === "darwin") {
    const sign = Bun.spawnSync(
      ["codesign", "--force", "--sign", "-", join(stage, "local-agent")],
      { stdout: "inherit", stderr: "inherit" },
    );
    if (sign.exitCode !== 0) throw new Error("codesign failed");
  }

  const ui = join(cli, "src/tuimark/ui");
  await cp(ui, join(stage, "ui"), {
    recursive: true,
    filter: (source) => shipped(source.slice(ui.length)),
  });
  for (const file of ["LICENSE", "README.md"])
    await cp(join(root, file), join(stage, file));

  const archive = join(out, `${name}.tar.gz`);
  // No owner names and no extended attributes (macOS records provenance
  // ones, which GNU tar warns about when it unpacks the archive): bsdtar on
  // macOS and GNU tar on Linux spell that differently.
  const anonymous =
    process.platform === "darwin"
      ? ["--no-xattrs", "--no-mac-metadata", "--uid", "0", "--gid", "0"]
      : ["--no-xattrs", "--owner=0", "--group=0", "--numeric-owner"];
  const tar = Bun.spawnSync(
    [
      "tar",
      ...anonymous,
      "-czf",
      archive,
      "-C",
      stage,
      "local-agent",
      "ui",
      "LICENSE",
      "README.md",
    ],
    {
      env: { ...process.env, COPYFILE_DISABLE: "1" },
      stdout: "inherit",
      stderr: "inherit",
    },
  );
  if (tar.exitCode !== 0) throw new Error("tar failed");
  console.log(archive);
}
