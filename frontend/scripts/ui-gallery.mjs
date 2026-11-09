import { mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import path from "node:path";

const directory = process.env.ZENTRALE_AUDIT_DIR ?? "artifacts/ui";
await mkdir(directory, { recursive: true });
const reports = await Promise.all(
  (await readdir(directory))
    .filter((file) => file.endsWith(".json"))
    .map(async (file) => ({
      variant: file.replace(/\.json$/, ""),
      ...JSON.parse(await readFile(path.join(directory, file), "utf8")),
    })),
);
const escapeHTML = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (character) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#39;",
      })[character],
  );
const count = reports.reduce(
  (total, report) => total + report.captures.length,
  0,
);
const cards = reports
  .flatMap((report) =>
    report.captures.map(
      (capture) => `
  <article data-variant="${escapeHTML(report.variant)}" data-name="${escapeHTML(capture.name)}">
    <a href="${escapeHTML(capture.file)}" target="_blank" rel="noreferrer">
      <img src="${escapeHTML(capture.file)}" alt="${escapeHTML(`${capture.name} · ${report.variant}`)}" loading="lazy">
    </a>
    <h2>${escapeHTML(capture.name)}</h2><p>${escapeHTML(report.variant)}</p>
  </article>`,
    ),
  )
  .join("");
await writeFile(
  path.join(directory, "index.html"),
  `<!doctype html>
<html lang="de"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Zentrale · UI-Prüfung</title>
<style>
  *{box-sizing:border-box}body{margin:0;background:#f5f5f7;color:#18181b;font:16px/1.5 system-ui,sans-serif}
  header{padding:24px;max-width:1600px;margin:auto}h1{font-size:28px;margin:0}header p{color:#52525b}
  .filters{display:flex;flex-wrap:wrap;gap:16px}label{display:flex;flex-direction:column;gap:8px}
  input,select{min-height:44px;padding:8px 12px;border:1px solid #858591;border-radius:10px;font:inherit;max-width:100%}
  main{max-width:1600px;margin:auto;padding:0 24px 32px;display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr));gap:24px}
  article{min-width:0;border:1px solid #dedee4;border-radius:16px;background:white;overflow:hidden}
  article[hidden]{display:none}a{display:grid;height:350px;place-items:start center;overflow:auto;background:#e8e8ee}
  img{display:block;max-width:100%;height:auto}h2{font-size:16px;margin:16px 16px 4px;overflow-wrap:anywhere}
  article p{margin:0 16px 16px;color:#52525b;font-size:14px}a:focus-visible{outline:3px solid #005fcc;outline-offset:-3px}
</style>
<header><h1>Zentrale · UI-Prüfung</h1><p>${count} Screenshots · Seiten, Dialoge, Formulare, Lade-, Leer- und Fehlerzustände. Ein Bild öffnet die vollständige Aufnahme.</p>
<div class="filters"><label>Ansicht<select id="variant"><option value="">Alle Ansichten</option>${reports.map((report) => `<option>${escapeHTML(report.variant)}</option>`).join("")}</select></label>
<label>Bereich suchen<input id="search" type="search" placeholder="z. B. provider, agent, empty"></label></div><p id="count" role="status">${count} Bilder</p></header>
<main>${cards}</main><script>
  const variant=document.getElementById('variant'),search=document.getElementById('search');
  function filter(){let count=0;document.querySelectorAll('article').forEach(card=>{card.hidden=Boolean((variant.value&&variant.value!==card.dataset.variant)||!card.dataset.name.toLowerCase().includes(search.value.toLowerCase()));if(!card.hidden)count++});document.getElementById('count').textContent=count+' Bilder'}
  variant.addEventListener('change',filter);search.addEventListener('input',filter);
</script></html>`,
);
console.log(
  `UI gallery: ${path.resolve(directory, "index.html")} (${count} screenshots)`,
);
