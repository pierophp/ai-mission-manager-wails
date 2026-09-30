import { describe, expect, it, vi } from "vitest";

const { on, unlisten } = vi.hoisted(() => ({ on: vi.fn(), unlisten: vi.fn() }));
vi.mock("@wailsio/runtime", () => ({
  Events: { On: on },
}));

import { listen } from "./event";

describe("Wails event adapter", () => {
  it("wraps event data as the Tauri payload and resolves to unlisten", async () => {
    let receive: ((event: { data: unknown }) => void) | undefined;
    on.mockImplementation((_name, callback) => {
      receive = callback;
      return unlisten;
    });
    const callback = vi.fn();

    const stop = await listen<{ runId: number }>("run-state-changed", callback);
    receive?.({ data: { runId: 12 } });

    expect(callback).toHaveBeenCalledWith({ payload: { runId: 12 } });
    expect(stop).toBe(unlisten);
  });
});
