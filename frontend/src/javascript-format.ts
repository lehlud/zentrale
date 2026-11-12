import { format } from "prettier/standalone";
import * as babel from "prettier/plugins/babel";
import * as estree from "prettier/plugins/estree";

// Display only: parsing never evaluates code or resolves the code's imports.
export async function formatJavaScriptForDisplay(source: string) {
  if (source.length > 65536) return source;
  try {
    return await format(source, {
      parser: "babel",
      plugins: [babel, estree],
      printWidth: 80,
      tabWidth: 2,
      useTabs: false,
      semi: true,
    });
  } catch {
    return source; // Invalid/incomplete historical calls stay inspectable.
  }
}
