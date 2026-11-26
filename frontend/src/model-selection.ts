import type { ModelChoice } from "./generated";

// The choice sent to mutations (without catalog presentation metadata).
export type ModelSelection = Pick<
  ModelChoice,
  "providerId" | "modelId" | "reasoningLevel"
>;

export type ModelSelections = {
  main: ModelSelection;
  approver: ModelSelection;
};

export function modelSelectionKey(choice: ModelSelection) {
  return choice.modelId
    ? JSON.stringify([choice.providerId, choice.modelId, choice.reasoningLevel])
    : "";
}
