import type { CommitIdentity } from "./generated";

function sameIdentity(
  left: CommitIdentity | undefined,
  right: CommitIdentity | undefined,
) {
  return left?.name === right?.name && left?.email === right?.email;
}

// These functions receive the CURRENT state from React's functional updater,
// not the draft captured when a save or passive effect was scheduled.
export function reconcileIdentityDraft(
  current: CommitIdentity | undefined,
  saved: CommitIdentity | undefined,
) {
  return sameIdentity(current, saved) ? undefined : current;
}

export function finishIdentitySave(
  current: CommitIdentity | undefined,
  submitted: CommitIdentity,
  normalized: CommitIdentity,
) {
  return current && sameIdentity(current, submitted) ? normalized : current;
}
