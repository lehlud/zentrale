import { readFileSync, writeFileSync } from "node:fs";

const schema = JSON.parse(
  readFileSync(new URL("../schema.json", import.meta.url), "utf8"),
).__schema;

const scalar = {
  String: "string",
  Int: "number",
  Float: "number",
  Boolean: "boolean",
  ID: "string",
};

function type(ref, nullable = true) {
  if (ref.kind === "NON_NULL") {
    return type(ref.ofType, false);
  }

  let value;
  if (ref.kind === "LIST") value = `Array<${type(ref.ofType)}>`;
  else value = scalar[ref.name] ?? ref.name;

  return nullable ? `${value} | null` : value;
}

let out = "// Generated from the Go GraphQL schema. Run make generate.\n";

for (const item of schema.types.sort((left, right) =>
  left.name.localeCompare(right.name),
)) {
  if (
    item.name.startsWith("__") ||
    !["OBJECT", "INPUT_OBJECT"].includes(item.kind)
  ) {
    continue;
  }

  out += `export type ${item.name} = {\n`;
  for (const field of (item.fields ?? item.inputFields ?? []).sort(
    (left, right) => left.name.localeCompare(right.name),
  )) {
    const optional =
      item.kind === "INPUT_OBJECT" && field.type.kind !== "NON_NULL" ? "?" : "";
    out += `  ${field.name}${optional}: ${type(field.type)};\n`;
  }

  out += "};\n";
}

writeFileSync(new URL("../src/generated.ts", import.meta.url), out);
