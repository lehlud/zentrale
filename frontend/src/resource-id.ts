// Matches domain.NewID: 96 random bits, encoded with a letter prefix as 17 URL-safe characters.
// These are opaque resource references, never authentication tokens.
export function newResourceId() {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  return (
    "r" +
    btoa(String.fromCharCode(...bytes))
      .replaceAll("+", "-")
      .replaceAll("/", "_")
  );
}
