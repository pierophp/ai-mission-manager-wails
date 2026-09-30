import { CommandService } from "../../bindings/github.com/piero/ai-mission-manager-wails/backend";

type CommandArguments = Record<string, unknown>;

export function invoke<T>(command: string, args?: CommandArguments): Promise<T> {
  return CommandService.Invoke(command, JSON.stringify(args ?? {})) as Promise<T>;
}
