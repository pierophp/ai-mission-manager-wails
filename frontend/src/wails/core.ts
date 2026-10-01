type CommandArguments = Record<string, unknown>;

export async function invoke<T>(command: string, args?: CommandArguments): Promise<T> {
  const { CommandService } = await import(
    "../../bindings/github.com/piero/ai-mission-manager-wails/backend"
  );
  return CommandService.Invoke(command, JSON.stringify(args ?? {})) as Promise<T>;
}
