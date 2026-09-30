import { PlatformService } from "../../bindings/github.com/piero/ai-mission-manager-wails/backend";

export function openUrl(url: string): Promise<void> {
  return PlatformService.OpenURL(url);
}

export function revealItemInDir(path: string): Promise<void> {
  return PlatformService.RevealItemInDir(path);
}
