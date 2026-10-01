import { beforeEach, describe, expect, it, vi } from "vitest";

const invokeMock = vi.hoisted(() => vi.fn(async () => undefined));

vi.mock("../wails/core", () => ({ invoke: invokeMock }));

import { commands } from "./bindings";

describe("generated command bindings over Wails", () => {
  beforeEach(() => invokeMock.mockClear());

  it("preserves command names and JSON argument keys for Grill and Plan", async () => {
    const configuration = { agent: "claude" as const, model: "claude-sonnet-5", effort: "high" };
    const request = {
      itemId: 1,
      workspaceId: 2,
      strategy: {
        kind: "grill" as const,
        machine_id: null,
        primary_repository_id: 3,
        configuration,
        language: "english" as const,
        prompt: "Start Grill",
        expected_checkouts: [],
        allow_dirty: false,
        allow_shared_checkouts: false,
      },
    };
    const answers = [{ questionNumber: 1, answer: "Choose A" }];

    await commands.composeGrillPrompt(1, configuration, "english", "Decide the design");
    await commands.prepareGrillRun(1, 2, null);
    await commands.startRun(request);
    await commands.submitGrillAnswers(9, answers);
    await commands.continueGrill(9, "to-spec");
    await commands.goPlan(10);

    expect(invokeMock.mock.calls).toEqual([
      ["compose_grill_prompt", { itemId: 1, configuration, language: "english", initialPrompt: "Decide the design" }],
      ["prepare_grill_run", { itemId: 1, workspaceId: 2, machineId: null }],
      ["start_run", { request }],
      ["submit_grill_answers", { runId: 9, answers }],
      ["continue_grill", { runId: 9, action: "to-spec" }],
      ["go_plan", { runId: 10 }],
    ]);
  });

  it("routes existing command bindings through the same Wails dispatcher", async () => {
    await commands.getHome(null, "2026-10-01T00:00:00Z");
    expect(invokeMock).toHaveBeenCalledWith("get_home", { contextId: null, now: "2026-10-01T00:00:00Z" });
  });
});
