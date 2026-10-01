import { invoke as invokeBackend } from "@tauri-apps/api/core";
import { commands } from "./bindings";

type CommandName = keyof typeof commands;
type CommandArguments<Name extends CommandName> = Parameters<(typeof commands)[Name]>;
type CommandResult<Name extends CommandName> = ReturnType<(typeof commands)[Name]>;

/** Invoke a generated command wrapper through the aliased Wails dispatcher. */
export function command<Name extends CommandName>(
  name: Name,
  ...args: CommandArguments<Name>
): CommandResult<Name> {
  return Reflect.apply(commands[name], undefined, args) as CommandResult<Name>;
}


/** Reveal a local Plan Run's reported path through the Wails opener adapter. */
export function invokeRevealPlan(runId: number): Promise<void> {
  return invokeBackend<void>("reveal_plan", { runId });
}
