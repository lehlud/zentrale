/* global importScripts, Module */

// One worker per preview. Closing the dialog drops its entire WASM heap.
let initialize;
const ready = new Promise((resolve) => {
  initialize = resolve;
});

self.Module = { onRuntimeInitialized: initialize };
importScripts("x2t.js");

const imageTypes = {
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
};

async function convert(extension, bytes) {
  await ready;
  if (!/^(docx?|xlsx?|pptx?)$/.test(extension))
    throw new Error("Unsupported Office format");

  const { FS } = Module;
  for (const directory of ["", "/media", "/themes"])
    FS.mkdir(`/working${directory}`);

  const input = `/working/document.${extension}`;
  const output = "/working/Editor.bin";
  FS.writeFile(input, new Uint8Array(bytes));
  FS.writeFile(
    "/working/params.xml",
    `<?xml version="1.0" encoding="utf-8"?>
    <TaskQueueDataConvert>
      <m_sFileFrom>${input}</m_sFileFrom>
      <m_sFileTo>${output}</m_sFileTo>
      <m_sThemeDir>/working/themes</m_sThemeDir>
      <m_bIsNoBase64>false</m_bIsNoBase64>
    </TaskQueueDataConvert>`,
  );

  const result = Module.ccall(
    "main1",
    "number",
    ["string"],
    ["/working/params.xml"],
  );
  if (result !== 0) throw new Error("Office conversion failed");

  const binary = FS.readFile(output).buffer;
  const images = [];
  for (const name of FS.readdir("/working/media")) {
    const type = imageTypes[name.split(".").at(-1)?.toLowerCase()];
    if (!type) continue;
    images.push([name, type, FS.readFile(`/working/media/${name}`).buffer]);
  }

  self.postMessage({ binary, images }, [
    binary,
    ...images.map((image) => image[2]),
  ]);
}

self.onmessage = ({ data }) => {
  convert(data.extension, data.bytes).catch(() =>
    self.postMessage({ error: true }),
  );
};
