import { expect, test } from "@playwright/test";

for (const locale of ["de", "en"]) {
  test(`jump-only setup and renamed SSH keys stay separate from project workers: ${locale}`, async ({
    page,
  }) => {
    const label = (de: string, en: string) => (locale === "de" ? de : en);
    await page.setViewportSize({
      width: locale === "de" ? 1440 : 390,
      height: 950,
    });
    await page.addInitScript(
      (language) => localStorage.setItem("zentrale-locale", language),
      locale,
    );
    const query = async (
      query: string,
      variables: Record<string, unknown> = {},
    ) => {
      const response = await page.request.post("/graphql", {
        data: { query, variables },
      });
      const result = await response.json();
      expect(result.errors).toBeUndefined();
      return result.data;
    };
    const status = await query("query{status{needsSetup}}");
    if (status.status.needsSetup)
      await query(
        'mutation{setup(password:"browser test password",commitIdentity:{name:"Test",email:"test@example.org"})}',
      );
    else await query('mutation{login(password:"browser test password")}');
    const { generateSSHKey: key } = await query(
      "mutation{generateSSHKey{id publicKey}}",
    );
    await page.goto("/#/keys");
    const original = label(`Schlüssel ${key.id}`, `Key ${key.id}`);
    await page
      .getByRole("button", {
        name: label(`Aktionen für ${original}`, `Actions for ${original}`),
        exact: true,
      })
      .click();
    await page
      .getByRole("menuitem", {
        name: label("Umbenennen", "Rename"),
        exact: true,
      })
      .click();
    let dialog = page.getByRole("dialog", {
      name: label("SSH-Schlüssel umbenennen", "Rename SSH key"),
      exact: true,
    });
    const keyName = `Login key ${locale} ${key.id}`;
    await dialog.getByLabel("Name", { exact: true }).fill(keyName);
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await expect(dialog).toHaveCount(0);
    await expect(
      page.getByRole("heading", { name: keyName, exact: true }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("heading", { name: keyName, exact: true }),
    ).toBeVisible();
    const { sshKeys: keys } = await query("query{sshKeys{id name publicKey}}");
    expect(
      keys.find((value: { id: number }) => value.id === key.id),
    ).toMatchObject({ name: keyName, publicKey: key.publicKey });

    await page.goto("/#/machines");
    await page
      .getByRole("button", {
        name: label("Maschine hinzufügen", "Add machine"),
        exact: true,
      })
      .click();
    dialog = page.getByRole("dialog", {
      name: label("Neue Fernmaschine", "New remote machine"),
      exact: true,
    });
    const bastionName = `Bastion ${locale} ${key.id}`;
    await dialog.getByLabel("Name", { exact: true }).fill(bastionName);
    await dialog
      .getByLabel("Host", { exact: true })
      .fill("bastion.example.test");
    await dialog
      .getByLabel(label("Benutzer", "User"), { exact: true })
      .fill("developer");
    await dialog
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await dialog
      .getByRole("button", { name: label("SSH-Schlüssel", "SSH key") })
      .and(dialog.locator('[data-slot="select-trigger"]'))
      .click();
    await page.getByRole("option", { name: keyName, exact: true }).click();
    await dialog
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await dialog
      .getByText(
        label("Nur als Jump-Host verwenden", "Use as jump host only"),
        { exact: true },
      )
      .click();
    await expect(
      dialog.getByRole("checkbox", {
        name: label("Nur als Jump-Host verwenden", "Use as jump host only"),
        exact: true,
      }),
    ).toBeChecked();
    await expect(
      dialog.getByLabel(
        label(
          "Persistentes Projektverzeichnis",
          "Persistent project directory",
        ),
        { exact: true },
      ),
    ).toHaveCount(0);
    await expect(
      dialog.getByRole("button", { name: "Sandbox", exact: true }),
    ).toHaveCount(0);
    await dialog
      .getByRole("button", { name: label("Speichern", "Save"), exact: true })
      .click();
    await expect(dialog).toHaveCount(0);
    const bastionCard = page.locator('[data-slot="card"]').filter({
      has: page.getByRole("heading", { name: bastionName, exact: true }),
    });
    await expect(
      bastionCard.getByText(label("Nur Jump-Host", "Jump host only"), {
        exact: true,
      }),
    ).toBeVisible();
    const { machines } = await query(
      "query{machines(limit:500){id name jumpHostOnly keyId}}",
    );
    const bastion = machines.find(
      (value: { name: string }) => value.name === bastionName,
    );
    expect(bastion).toMatchObject({ jumpHostOnly: true, keyId: key.id });
    const { generateSSHKey: workerKey } = await query(
      "mutation{generateSSHKey{id}}",
    );
    const workerName = `Worker ${locale} ${key.id}`;
    const { saveMachine: worker } = await query(
      "mutation($input:MachineInput!){saveMachine(input:$input){id}}",
      {
        input: {
          name: workerName,
          host: "worker.example.test",
          user: "developer",
          keyId: workerKey.id,
          jumps: [bastion.id],
        },
      },
    );
    await page.goto("/#/projects");
    await page
      .getByRole("button", {
        name: label("Projekt anlegen", "Create project"),
        exact: true,
      })
      .click();
    dialog = page.getByRole("dialog");
    await dialog.getByLabel("Name", { exact: true }).fill("Jump role check");
    await dialog
      .getByRole("button", { name: label("Weiter", "Continue"), exact: true })
      .click();
    await dialog
      .getByRole("button", {
        name: label("Fernmaschine oder Pool", "Remote machine or pool"),
      })
      .and(dialog.locator('[data-slot="select-trigger"]'))
      .click();
    await expect(
      page.getByRole("option", {
        name: `${label("Fernmaschine", "Remote machine")}: ${bastionName}`,
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByRole("option", {
        name: `${label("Fernmaschine", "Remote machine")}: ${workerName}`,
        exact: true,
      }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await dialog
      .getByRole("button", { name: label("Abbrechen", "Cancel"), exact: true })
      .click();
    for (const id of [worker.id, bastion.id])
      await query(
        'mutation($id:String!){deleteResource(kind:"machine",id:$id)}',
        { id },
      );
    for (const id of [key.id, workerKey.id])
      await query("mutation($id:Int!){deleteSSHKey(id:$id)}", { id });
  });
}
