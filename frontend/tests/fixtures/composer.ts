import type { Page } from "@playwright/test";

export async function openComposerCommand(page: Page, name: string) {
  const composer = page.getByRole("textbox", {
    name: /Aufgabe oder neue Instruktion|Task or new instruction/,
  });
  await composer.fill(`/${name}`);
  await composer.press("Enter");
}
