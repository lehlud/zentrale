import type { Field } from "./ui";

export function validateFields(
  fields: Field[],
  values: Record<string, unknown>,
): Record<string, string> {
  const invalid: Record<string, string> = {};
  for (const field of fields) {
    const value = values[field.key];
    if (
      field.required &&
      (value == null ||
        (typeof value === "string" && value.trim() === "") ||
        (Array.isArray(value) && value.length === 0))
    )
      invalid[field.key] = "Dieses Feld ist erforderlich.";

    if (
      field.type === "number" &&
      value != null &&
      !Number.isFinite(Number(value))
    )
      invalid[field.key] = "Eine gültige Zahl eingeben.";
  }

  return invalid;
}

// Server errors that identify a field are placed next to that control. Errors
// without a clear field remain at form level rather than guessing a location.
export function fieldForServerError(
  message: string,
  fields: Field[],
): string | undefined {
  const candidates: [RegExp, string][] = [
    [/OAuth.*client|client.?id/i, "clientId"],
    [/Git service requires an HTTPS origin/i, "baseUrl"],
    [/repository name/i, "name"],
    [/select an owner|selected repository owner/i, "namespaceId"],
    [/commit name/i, "commitName"],
    [/commit email/i, "commitEmail"],
    [/context window/i, "contextWindow"],
    [/token prices/i, "inputPrice"],
    [/SSH port/i, "port"],
    [/machine host/i, "host"],
    [/machine user/i, "user"],
    [/machine name/i, "name"],
    [/SSH key/i, "keyId"],
    [/microvm_command/i, "microVMCommand"],
    [/gpu_devices/i, "gpuDevicesText"],
    [/model/i, "model"],
    [/provider URL/i, "baseUrl"],
    [/workspace|project directory/i, "directory"],
    [/workspace root|workspace_root/i, "workspaceRoot"],
    [/select a remote machine or pool/i, "machineIds"],
    [/YAML|pool configuration/i, "yaml"],
  ];
  return candidates.find(
    ([pattern, key]) =>
      pattern.test(message) && fields.some((field) => field.key === key),
  )?.[1];
}
