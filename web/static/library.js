// Folio library page: lazy first-page covers and keyboard shortcuts.
import * as pdfjsLib from "/static/vendor/pdfjs/pdf.min.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/static/vendor/pdfjs/pdf.worker.min.mjs";

const COVER_WIDTH = 240; // CSS pixels
const MAX_PARALLEL = 2;

const queue = [];
const seen = new WeakSet();
let active = 0;

async function drawCover(canvas) {
  const doc = await pdfjsLib.getDocument({ url: canvas.dataset.cover, disableAutoFetch: true }).promise;
  try {
    const page = await doc.getPage(1);
    const base = page.getViewport({ scale: 1 });
    const ratio = window.devicePixelRatio || 1;
    const viewport = page.getViewport({ scale: (COVER_WIDTH / base.width) * ratio });
    canvas.width = Math.floor(viewport.width);
    canvas.height = Math.floor(viewport.height);
    await page.render({ canvas, viewport }).promise;
    canvas.classList.add("ready");
  } finally {
    doc.destroy();
  }
}

function pump() {
  while (active < MAX_PARALLEL && queue.length) {
    const canvas = queue.shift();
    active++;
    drawCover(canvas)
      .catch((err) => console.warn("cover", canvas.dataset.cover, err))
      .finally(() => { active--; pump(); });
  }
}

const observer = new IntersectionObserver((entries) => {
  for (const entry of entries) {
    if (!entry.isIntersecting) continue;
    observer.unobserve(entry.target);
    queue.push(entry.target);
  }
  pump();
}, { rootMargin: "300px" });

function watchCovers() {
  for (const canvas of document.querySelectorAll("canvas[data-cover]")) {
    if (seen.has(canvas)) continue;
    seen.add(canvas);
    observer.observe(canvas);
  }
}

watchCovers();
// Datastar patches the library after scans and tag edits; pick up new cards.
new MutationObserver(watchCovers).observe(document.body, { childList: true, subtree: true });

document.addEventListener("keydown", (e) => {
  const t = e.target;
  if (t.closest && t.closest("input, textarea, select, [contenteditable]")) {
    if (e.key === "Escape") t.blur();
    return;
  }
  if (e.key === "/" && !e.ctrlKey && !e.metaKey) {
    e.preventDefault();
    document.getElementById("search").focus();
  }
});
