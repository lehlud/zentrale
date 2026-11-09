import * as monaco from "monaco-editor/editor/editor.api";
import "monaco-editor/languages/definitions/yaml/register";
import "monaco-editor/editor/contrib/find/browser/findController";
import "monaco-editor/editor/contrib/folding/browser/folding";
import "monaco-editor/editor/contrib/bracketMatching/browser/bracketMatching";
import "monaco-editor/editor/contrib/linesOperations/browser/linesOperations";
import "monaco-editor/editor/contrib/indentation/browser/indentation";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";

// Vite emits a same-origin worker; editing never needs a CDN or remote service.
globalThis.MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
};

export { monaco };
