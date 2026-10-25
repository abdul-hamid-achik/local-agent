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
  // Only a word right after the slash is a command. A pasted path or a URL
  // ("/tmp/log.txt explain this") is a prompt, and goes to the model.
  if (/^\/[a-z][\w-]*(?:\s|$)/i.test(command))
    return { kind: "unknown", command };
  return { kind: "not-slash" };
}
