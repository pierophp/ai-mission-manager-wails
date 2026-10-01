import { beforeEach, describe, expect, it, vi } from "vitest";

const invokeMock = vi.hoisted(() => vi.fn(async () => undefined));

vi.mock("../../bindings/github.com/piero/ai-mission-manager-wails/backend", () => ({
  CommandService: { Invoke: invokeMock },
}));

import { invoke } from "./core";

describe("Wails command transport", () => {
  beforeEach(() => invokeMock.mockClear());

  it("serializes the command arguments for the single dispatcher", async () => {
    await invoke("continue_grill", { runId: 9, action: "to-spec" });
    await invoke("list_contexts");
    await invoke("reveal_plan", { runId: 10 });

    expect(invokeMock.mock.calls).toEqual([
      ["continue_grill", '{"runId":9,"action":"to-spec"}'],
      ["list_contexts", "{}"],
      ["reveal_plan", '{"runId":10}'],
    ]);
  });
});
