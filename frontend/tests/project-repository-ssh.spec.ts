import { expect, test } from "@playwright/test";

test("project settings save a direct SSH repository and let users generate a replacement key", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await page.addInitScript(() => localStorage.setItem("zentrale-locale", "de"));
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
  const { status } = await query("query{status{needsSetup}}");
  await query(
    status.needsSetup
      ? 'mutation{setup(password:"browser test password",commitIdentity:{name:"Test",email:"test@example.org"})}'
      : 'mutation{login(password:"browser test password")}',
  );

  const generate = "mutation{generateSSHKey{id}}";
  const { generateSSHKey: workerKey } = await query(generate);
  const { generateSSHKey: repositoryKey } = await query(generate);
  const keyName = `Git repository key ${repositoryKey.id}`;
  await query(
    "mutation($id:Int!,$name:String!){renameSSHKey(id:$id,name:$name)}",
    {
      id: repositoryKey.id,
      name: keyName,
    },
  );
  const { saveMachine: machine } = await query(
    'mutation($key:Int!){saveMachine(input:{name:"SSH project worker",host:"worker.example.test",user:"developer",keyId:$key}){id}}',
    { key: workerKey.id },
  );
  const projectName = `SSH project ${repositoryKey.id}`;
  const { saveProject: project } = await query(
    "mutation($input:ProjectInput!){saveProject(input:$input){id}}",
    { input: { name: projectName, machineIds: [machine.id] } },
  );

  await page.goto("/#/projects");
  const edit = async () => {
    await page
      .getByRole("button", { name: `Aktionen für ${projectName}`, exact: true })
      .click();
    await page
      .getByRole("menuitem", { name: "Bearbeiten", exact: true })
      .click();
    return page.getByRole("dialog", {
      name: "Projekt bearbeiten",
      exact: true,
    });
  };
  const read = () =>
    query(
      "query($id:String!){project(id:$id){repositorySshUrl repositorySshKeyId repositoryId} sources(projectId:$id){url keyId projectRoot}}",
      { id: project.id },
    );
  let dialog = await edit();
  await dialog
    .getByRole("button", { name: "SSH-URL eingeben", exact: true })
    .click();
  const firstURL = "git@git.example.test:team/repository.git";
  await dialog.getByLabel("Repository-SSH-URL").fill(firstURL);
  const keySelect = () =>
    dialog
      .getByRole("button", { name: "SSH-Schlüssel" })
      .and(dialog.locator('[data-slot="select-trigger"]'));
  await keySelect().click();
  await page.getByRole("option", { name: keyName, exact: true }).click();
  const save = () =>
    dialog.getByRole("button", { name: "Speichern", exact: true }).click();
  await save();
  await expect(dialog).toHaveCount(0);
  expect(await read()).toMatchObject({
    project: {
      repositorySshUrl: firstURL,
      repositorySshKeyId: repositoryKey.id,
      repositoryId: "",
    },
    sources: [{ url: firstURL, keyId: repositoryKey.id, projectRoot: true }],
  });

  dialog = await edit();
  await expect(dialog.getByLabel("Repository-SSH-URL")).toHaveValue(firstURL);
  await expect(keySelect()).toContainText(keyName);
  const secondURL = "ssh://git@git.example.test:2222/team/repository.git";
  await dialog.getByLabel("Repository-SSH-URL").fill(secondURL);
  await dialog
    .getByRole("button", { name: "Neuen SSH-Schlüssel erzeugen", exact: true })
    .click();
  await expect(keySelect()).toContainText(/^Schlüssel \d+$/);
  await expect(
    dialog.getByRole("button", {
      name: "Öffentlichen Schlüssel kopieren",
      exact: true,
    }),
  ).toBeVisible();
  await save();
  await expect(dialog).toHaveCount(0);
  const replaced = await read();
  expect(replaced.project.repositorySshKeyId).toBeGreaterThan(0);
  expect(replaced.project.repositorySshKeyId).not.toBe(repositoryKey.id);
  expect(replaced.sources).toEqual([
    {
      url: secondURL,
      keyId: replaced.project.repositorySshKeyId,
      projectRoot: true,
    },
  ]);

  dialog = await edit();
  await dialog
    .getByRole("button", { name: "Auswahl entfernen", exact: true })
    .click();
  await save();
  await expect(dialog).toHaveCount(0);
  expect(await read()).toMatchObject({
    project: { repositorySshUrl: "", repositorySshKeyId: 0 },
    sources: [],
  });
});
