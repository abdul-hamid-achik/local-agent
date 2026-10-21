/** Pure slash-command parser shared by the Chat composer. */

export type SlashCommand =
  | { kind: "mode"; requested: string | undefined }
  | { kind: "help" }
  | { kind: "mcp" }
  | { kind: "unknown"; command: string }
  | { kind: "not-slash" };

export function parseSlashCommand(input: string): SlashCommand {
  const command = input.trim();
  const modeCommand = command.match(/^\/mode(?:\s+(\S+))?\s*$/i);
  if (modeCommand)
    return { kind: "mode", requested: modeCommand[1]?.toLowerCase() };
  if (command === "/help") return { kind: "help" };
  if (command === "/mcp") return { kind: "mcp" };
  if (command.startsWith("/")) return { kind: "unknown", command };
  return { kind: "not-slash" };
}
