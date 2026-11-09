import { expect, test } from "@playwright/test";

import { chooseContextAction } from "./fixtures/context-actions";

test("commit identity settings preserve errors and project overrides can return to inheritance", async ({
  page,
}) => {
  await page.addInitScript(() => {
    localStorage.setItem("zentrale-locale", "de");
    localStorage.setItem("zentrale-theme", "light");
  });
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
  if (status.status.needsSetup) {
    await query(
      'mutation{setup(password:"browser test password",commitIdentity:{name:"Initial Team",email:"initial@example.test"})}',
    );
  } else {
    await query('mutation{login(password:"browser test password")}');
  }

  await page.goto("/#/settings");
  const settings = page.getByRole("region", {
    name: "Standard-Identität für Commits",
    exact: true,
  });
  const name = settings.getByLabel("Name für Commits", { exact: true });
  const email = settings.getByLabel("E-Mail für Commits", { exact: true });
  await name.fill("Ungültiger <Name>");
  await email.fill("global@example.test");
  await settings
    .getByRole("button", { name: "Commit-Identität speichern", exact: true })
    .click();

  await expect(settings.getByText(/Einen Commit-Namen mit/)).toBeVisible();
  await expect(name).toBeFocused();
  await expect(email).toHaveValue("global@example.test");

  await name.fill("  Globales Projektteam  ");
  await settings
    .getByRole("button", { name: "Commit-Identität speichern", exact: true })
    .click();

  await expect(
    settings.getByRole("button", {
      name: "Commit-Identität speichern",
      exact: true,
    }),
  ).toBeDisabled();
  expect(
    (await query("query{commitIdentity{name email}}")).commitIdentity,
  ).toEqual({ name: "Globales Projektteam", email: "global@example.test" });

  await name.fill("Nur ein Entwurf");

  await expect(settings.getByRole("status")).toHaveText(
    "Nicht gespeicherte Änderungen",
  );

  await settings
    .getByRole("button", { name: "Verwerfen", exact: true })
    .click();

  await expect(name).toHaveValue("Globales Projektteam");
  await expect(email).toHaveValue("global@example.test");
  await expect(settings.getByRole("status")).toHaveCount(0);
  expect(
    (await query("query{commitIdentity{name email}}")).commitIdentity,
  ).toEqual({ name: "Globales Projektteam", email: "global@example.test" });

  const { generateSSHKey: key } = await query("mutation{generateSSHKey{id}}");
  const { saveMachine: machine } = await query(
    "mutation($input:MachineInput!){saveMachine(input:$input){id}}",
    {
      input: {
        id: "commit-identity-machine",
        name: "Commit-Testmaschine",
        host: "127.0.0.1",
        port: 22,
        user: "test",
        keyId: key.id,
        sandbox: "none",
      },
    },
  );
  const projectId = "commit-identity-project";
  await query("mutation($input:ProjectInput!){saveProject(input:$input){id}}", {
    input: {
      id: projectId,
      name: "Projekt mit eigener Commit-Identität",
      directory: "/srv/commit-identity-test",
      machineIds: [machine.id],
      poolIds: [],
    },
  });
  await page.goto("/#/projects");
  const project = page.locator("section").filter({
    has: page.getByRole("link", {
      name: "Projekt mit eigener Commit-Identität",
      exact: true,
    }),
  });
  await chooseContextAction(page, project, "Bearbeiten");
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("Commit-Name (optional)", { exact: true })
    .fill("Projektteam");
  await dialog
    .getByLabel("Commit-E-Mail (optional)", { exact: true })
    .fill("project@example.test");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).toHaveCount(0);

  const loadProject = async () =>
    (
      await query(
        "query($id:String!){project(id:$id){commitName commitEmail}}",
        { id: projectId },
      )
    ).project;

  expect(await loadProject()).toEqual({
    commitName: "Projektteam",
    commitEmail: "project@example.test",
  });

  await chooseContextAction(page, project, "Bearbeiten");
  await dialog.getByLabel("Commit-Name (optional)", { exact: true }).fill("");
  await dialog.getByLabel("Commit-E-Mail (optional)", { exact: true }).fill("");
  await dialog.getByRole("button", { name: "Speichern", exact: true }).click();

  await expect(dialog).toHaveCount(0);
  expect(await loadProject()).toEqual({ commitName: "", commitEmail: "" });
});
