// A host that is running a command with a sleeping descendant when it goes
// away. Usage: bun detached_orphan.ts <pidfile> <exit|signal|handled>.
// `exit` leaves through process.exit, `signal` waits to be signalled, and
// `handled` has a SIGTERM handler of its own, as the CLI does.
import { readFileSync } from "node:fs";
import {
  createCommandProposal,
  executeApprovedCommand,
} from "#runtime/command";

const [pidfile, mode] = process.argv.slice(2);
if (!pidfile) throw new Error("missing pidfile");

if (mode === "handled")
  process.once("SIGTERM", () => setTimeout(() => process.exit(7), 100));

const proposal = await createCommandProposal(
  [
    "/bin/sh",
    "-c",
    mode === "stubborn"
      ? `(trap '' TERM; sleep 300) & echo $! > '${pidfile}'; wait`
      : `sleep 300 & echo $! > '${pidfile}'; wait`,
  ],
  process.cwd(),
  600_000,
);
void executeApprovedCommand(proposal, new AbortController().signal);

const poll = setInterval(() => {
  // The shell creates the file before it writes the pid.
  try {
    if (!readFileSync(pidfile, "utf8").trim()) return;
  } catch {
    return;
  }
  clearInterval(poll);
  console.log("ready");
  if (mode === "exit" || mode === "stubborn") process.exit(3);
}, 10);
