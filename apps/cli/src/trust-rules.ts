// `local-agent --trust-rules`: the terminal twin of `/permissions trust`.
// It lists the workspace's untrusted allow rules and trusts exactly those,
// and only after the user types "yes" at a terminal: a script or a pipe
// cannot answer for them.

import { createInterface } from "node:readline/promises";
import {
  loadPermissionFile,
  permissionsPath,
  trustRules,
} from "@local-agent/runtime";
import { terminalText } from "#cli/text";

export type TrustIO = {
  print(line: string): void;
  /** The user's answer, or undefined when there is no terminal to ask. */
  ask(question: string): Promise<string | undefined>;
};

/** Runs the review and the confirmation; returns the exit code. */
export async function trustRulesCommand(
  root: string,
  io: TrustIO,
): Promise<number> {
  const file = permissionsPath(root);
  const loaded = await loadPermissionFile(root);
  if (loaded.untrusted.length === 0) {
    // A broken file says why; a damaged trust store has untrusted rules.
    io.print(
      loaded.warning
        ? terminalText(loaded.warning)
        : `No untrusted allow rules in ${file}`,
    );
    return loaded.warning ? 1 : 0;
  }
  const count = loaded.untrusted.length;
  io.print(
    `${count} allow rule${count === 1 ? "" : "s"} in ${file} not trusted on this machine:`,
  );
  for (const rule of loaded.untrusted)
    io.print(`  allow ${rule.tool} ${JSON.stringify(rule.pattern)}`);
  const answer = await io.ask(`Trust ${count === 1 ? "it" : "them"}? [yes/N] `);
  if (answer === undefined) {
    io.print("--trust-rules asks for confirmation; run it in a terminal");
    return 1;
  }
  if (answer.trim().toLowerCase() !== "yes") {
    io.print("Nothing trusted");
    return 1;
  }
  const { trusted } = await trustRules(root, loaded.untrusted);
  io.print(
    trusted.length
      ? `Trusted ${trusted.length} rule${trusted.length === 1 ? "" : "s"}`
      : "Nothing trusted: the rules changed while you were reading them",
  );
  return trusted.length ? 0 : 1;
}

/** The process's own terminal, when both ends are one. */
export function terminalTrustIO(): TrustIO {
  return {
    print: (line) => console.log(line),
    ask: async (question) => {
      if (!process.stdin.isTTY || !process.stdout.isTTY) return undefined;
      const prompt = createInterface({
        input: process.stdin,
        output: process.stdout,
      });
      try {
        return await prompt.question(question);
      } finally {
        prompt.close();
      }
    },
  };
}
