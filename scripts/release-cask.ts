// Prints the Homebrew cask for a release, its sums read from the release's
// SHA256SUMS.txt (release.yml pushes it to abdul-hamid-achik/homebrew-tap):
//
//   bun scripts/release-cask.ts v0.52.0 SHA256SUMS.txt > Casks/local-agent.rb
//
// The archives are flat, so `binary "local-agent"` is the same path on every
// platform, and ui/ stays next to the executable in the Caskroom, where the
// executable finds it through the symlink Homebrew puts on PATH.

/** The cask's platform blocks, in the order the tap's other casks use. */
const PLATFORMS = [
  ["on_macos", "on_arm", "darwin_arm64"],
  ["on_macos", "on_intel", "darwin_amd64"],
  ["on_linux", "on_arm", "linux_arm64"],
  ["on_linux", "on_intel", "linux_amd64"],
] as const;

/** name -> sha256 from a `sha256sum` file. */
export function parseSums(text: string): Map<string, string> {
  const sums = new Map<string, string>();
  for (const line of text.split("\n")) {
    const match = /^([0-9a-f]{64}) [ *](\S+)$/.exec(line.trim());
    if (match?.[1] && match[2]) sums.set(match[2], match[1]);
  }
  return sums;
}

export function cask(tag: string, sums: Map<string, string>): string {
  const version = tag.replace(/^v/, "");
  if (!/^\d+\.\d+\.\d+$/.test(version))
    throw new Error(`not a version: ${tag}`);
  const block = (os: string) =>
    PLATFORMS.filter(([on]) => on === os)
      .map(([, cpu, target]) => {
        const file = `local-agent_${version}_${target}.tar.gz`;
        const sum = sums.get(file);
        if (!sum) throw new Error(`no checksum for ${file}`);
        return [
          `    ${cpu} do`,
          `      sha256 "${sum}"`,
          `      url "https://github.com/abdul-hamid-achik/local-agent/releases/download/v#{version}/local-agent_#{version}_${target}.tar.gz"`,
          "    end",
        ].join("\n");
      })
      .join("\n");
  return `# Written by local-agent's release workflow (scripts/release-cask.ts).
cask "local-agent" do
  version "${version}"

  on_macos do
${block("on_macos")}
  end
  on_linux do
${block("on_linux")}
  end

  name "local-agent"
  desc "Local coding agent for Ollama, with a terminal UI and MCP tools"
  homepage "https://local-agent.dev/"

  livecheck do
    url :url
    strategy :github_latest
  end

  binary "local-agent"

  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}"]
    end
  end

  # No zap stanza required
end
`;
}

if (import.meta.main) {
  const [tag, sumsFile] = Bun.argv.slice(2);
  if (!tag || !sumsFile)
    throw new Error("usage: release-cask.ts TAG SHA256SUMS.txt");
  process.stdout.write(cask(tag, parseSums(await Bun.file(sumsFile).text())));
}
