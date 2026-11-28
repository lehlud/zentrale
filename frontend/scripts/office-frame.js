// This script runs inside the opaque editor frame, not in the application.
const parentOrigin = new URL(location.href).searchParams.get("parentOrigin");
const images = new Map();

window.APP = {
  getImageURL(path, done) {
    const name = String(path).split("/").at(-1);
    done(images.get(name) ?? "");
  },
};

window.addEventListener("message", (event) => {
  if (event.source !== parent || event.origin !== parentOrigin) return;
  if (event.data?.type !== "zentrale:office-images") return;

  for (const [name, type, bytes] of event.data.images) {
    const url = URL.createObjectURL(new Blob([bytes], { type }));
    images.set(name, url);
  }
});

window.addEventListener(
  "keydown",
  (event) => {
    if (event.key !== "Escape" || event.repeat) return;
    // Give the editor's menus and controls the first chance to handle Escape.
    setTimeout(() => {
      if (!event.defaultPrevented)
        parent.postMessage({ type: "zentrale:office-dismiss" }, parentOrigin);
    }, 0);
  },
  true,
);
