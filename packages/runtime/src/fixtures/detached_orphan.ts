// A host that is running a command with a sleeping descendant when it goes
// away. Usage: bun detached_orphan.ts <pidfile> <mode>, where mode is
// exit | stubborn | signal | handled | kept | cancel. `exit` leaves through
// process.exit (`stubborn` too, with a descendant that ignores SIGTERM),
// `signal` waits to be signalled, and the rest have a SIGTERM handler of
// their own, as the CLI does.
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
const cancel = new AbortController();
const running = executeApprovedCommand(proposal, cancel.signal);
// The host keeps running after the signal and decides itself: `kept` exits a
// second later (the exit hook cleans up), `cancel` cancels the command and
// leaves when it has ended.
if (mode === "kept")
  process.once("SIGTERM", () => setTimeout(() => process.exit(9), 1000));
if (mode === "cancel")
  process.once("SIGTERM", () => {
    cancel.abort();
    void running.then(() => process.exit(8));
  });

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
