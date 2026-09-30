import { PlatformService } from "../../bindings/github.com/piero/ai-mission-manager-wails/backend";

export type OpenDialogOptions = {
  directory?: boolean;
  multiple?: boolean;
  title?: string;
};

export function open(
  options: OpenDialogOptions = {},
): Promise<string | string[] | null> {
  if (options.directory === false) {
    return Promise.reject(new Error("Only directory selection is supported"));
  }
  return PlatformService.OpenDirectory({
    title: options.title ?? "Select a directory",
    multiple: options.multiple ?? false,
  }) as Promise<string | string[] | null>;
}
