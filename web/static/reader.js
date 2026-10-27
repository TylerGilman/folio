// Folio reader: renders one PDF page at a time with PDF.js, handles paging,
// zoom and keyboard shortcuts, and saves the reading position to the server.
import * as pdfjsLib from "/static/vendor/pdfjs/pdf.min.mjs";

pdfjsLib.GlobalWorkerOptions.workerSrc = "/static/vendor/pdfjs/pdf.worker.min.mjs";

const SAVE_DELAY_MS = 700;
const ZOOM_STEP = 1.2;

const viewer = document.getElementById("viewer");
const canvas = document.getElementById("page-canvas");
const msg = document.getElementById("viewer-msg");
const pageInput = document.getElementById("page-input");
const pageCount = document.getElementById("page-count");
const saveState = document.getElementById("save-state");
const zoomLabel = document.getElementById("zoom-label");
const notePage = document.getElementById("note-page");
const notePageLabel = document.getElementById("note-page-label");
const notesPanel = document.getElementById("notes-panel");

const bookId = viewer.dataset.book;
const positionURL = `/api/books/${bookId}/position`;
const zoomKey = `folio:zoom:${bookId}`;

let pdf = null;
let page = Math.max(1, parseInt(viewer.dataset.page, 10) || 1);
let zoom = parseFloat(storageGet(zoomKey)) || 1; // 1 = fit width
let rendering = false;
let pendingRender = false;
let saveTimer = null;
let dirty = false;

function storageGet(key) {
  try { return localStorage.getItem(key); } catch { return null; }
}
function storageSet(key, value) {
  try { localStorage.setItem(key, value); } catch { /* storage unavailable */ }
}

async function render() {
  if (!pdf) return;
  if (rendering) {
    pendingRender = true;
    return;
  }
  rendering = true;
  try {
    const p = await pdf.getPage(page);
    const base = p.getViewport({ scale: 1 });
    // zoom 1 fits the whole page in the viewer.
    const fitWidth = Math.max(200, viewer.clientWidth - 32) / base.width;
    const fitHeight = Math.max(200, viewer.clientHeight - 32) / base.height;
    const scale = Math.min(fitWidth, fitHeight) * zoom;
    const viewport = p.getViewport({ scale });
    const ratio = window.devicePixelRatio || 1;
    canvas.width = Math.floor(viewport.width * ratio);
    canvas.height = Math.floor(viewport.height * ratio);
    canvas.style.width = `${Math.floor(viewport.width)}px`;
    canvas.style.height = `${Math.floor(viewport.height)}px`;
    await p.render({
      canvas,
      viewport,
      transform: ratio !== 1 ? [ratio, 0, 0, ratio, 0, 0] : undefined,
    }).promise;
    msg.hidden = true;
  } catch (err) {
    showError(err);
  } finally {
    rendering = false;
    if (pendingRender) {
      pendingRender = false;
      render();
    }
  }
}

function showError(err) {
  console.error(err);
  msg.hidden = false;
  msg.textContent = `Could not show this PDF: ${err.message || err}`;
}

function updatePageUI() {
  pageInput.value = page;
  notePage.value = page;
  notePageLabel.textContent = page;
  document.title = document.title.replace(/^(p\. \d+ · )?/, `p. ${page} · `);
  markCurrentNotes();
}

// Only toggles classes, so it is safe to call from the MutationObserver below.
function markCurrentNotes() {
  for (const li of document.querySelectorAll("#notes li[data-page]")) {
    li.classList.toggle("current", li.dataset.page === String(page));
  }
}

function goTo(n) {
  if (!pdf) return;
  const next = Math.min(Math.max(1, n), pdf.numPages);
  if (next === page) {
    updatePageUI();
    return;
  }
  page = next;
  updatePageUI();
  viewer.scrollTop = 0;
  render();
  scheduleSave();
}

function setZoom(z) {
  zoom = Math.min(Math.max(z, 0.25), 6);
  zoomLabel.textContent = Math.abs(zoom - 1) < 0.01 ? "Fit" : `${Math.round(zoom * 100)}%`;
  storageSet(zoomKey, String(zoom));
  render();
}

// --- position saving ---------------------------------------------------

function positionBody() {
  return JSON.stringify({ page, pages: pdf.numPages });
}

function scheduleSave() {
  dirty = true;
  saveState.textContent = "";
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveNow, SAVE_DELAY_MS);
}

async function saveNow() {
  clearTimeout(saveTimer);
  if (!pdf || !dirty) return;
  dirty = false;
  try {
    const res = await fetch(positionURL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: positionBody(),
      keepalive: true,
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    saveState.textContent = "Saved";
  } catch (err) {
    dirty = true;
    saveState.textContent = "Not saved";
    console.error("save position", err);
  }
}

// On leave, fetch may be cancelled; sendBeacon is delivered by the browser.
function flushOnLeave() {
  if (!pdf || !dirty) return;
  clearTimeout(saveTimer);
  const blob = new Blob([positionBody()], { type: "text/plain" });
  if (navigator.sendBeacon(positionURL, blob)) dirty = false;
}

window.addEventListener("pagehide", flushOnLeave);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "hidden") flushOnLeave();
});

// --- controls ----------------------------------------------------------

document.getElementById("prev").addEventListener("click", () => goTo(page - 1));
document.getElementById("next").addEventListener("click", () => goTo(page + 1));
document.getElementById("zoom-in").addEventListener("click", () => setZoom(zoom * ZOOM_STEP));
document.getElementById("zoom-out").addEventListener("click", () => setZoom(zoom / ZOOM_STEP));
document.getElementById("zoom-fit").addEventListener("click", () => setZoom(1));
document.getElementById("jump").addEventListener("submit", (e) => {
  e.preventDefault();
  goTo(parseInt(pageInput.value, 10) || page);
  viewer.focus();
});
pageInput.addEventListener("change", () => goTo(parseInt(pageInput.value, 10) || page));

const notesHiddenKey = "folio:notes-hidden";
function setNotesHidden(hidden) {
  document.body.classList.toggle("notes-hidden", hidden);
  storageSet(notesHiddenKey, hidden ? "1" : "");
  render();
}
setNotesHidden(storageGet(notesHiddenKey) === "1");
document.getElementById("toggle-notes").addEventListener("click", () =>
  setNotesHidden(!document.body.classList.contains("notes-hidden")));

// Note page links: jump to the note's page.
notesPanel.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-goto]");
  if (btn) goTo(parseInt(btn.dataset.goto, 10));
});
// Re-mark current-page notes after Datastar patches the list.
new MutationObserver(markCurrentNotes).observe(notesPanel, { childList: true, subtree: true });

document.addEventListener("keydown", (e) => {
  const t = e.target;
  const typing = t.closest && t.closest("input, textarea, select, [contenteditable]");
  if (typing) {
    if (e.key === "Escape") t.blur();
    return;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  switch (e.key) {
    case "ArrowRight": case "PageDown": case "j": case " ":
      if (e.key === " " && e.shiftKey) goTo(page - 1); else goTo(page + 1);
      break;
    case "ArrowLeft": case "PageUp": case "k":
      goTo(page - 1);
      break;
    case "Home": goTo(1); break;
    case "End": if (pdf) goTo(pdf.numPages); break;
    case "+": case "=": setZoom(zoom * ZOOM_STEP); break;
    case "-": setZoom(zoom / ZOOM_STEP); break;
    case "0": setZoom(1); break;
    case "g": pageInput.focus(); pageInput.select(); break;
    case "n":
      if (document.body.classList.contains("notes-hidden")) setNotesHidden(false);
      document.getElementById("note-body").focus();
      break;
    case "N": setNotesHidden(!document.body.classList.contains("notes-hidden")); break;
    case "Escape": window.location.href = "/"; break;
    default: return;
  }
  e.preventDefault();
});

let resizeTimer = null;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(render, 150);
});

// --- load --------------------------------------------------------------

async function load() {
  setZoom(zoom);
  try {
    pdf = await pdfjsLib.getDocument({ url: viewer.dataset.src, disableAutoFetch: true }).promise;
  } catch (err) {
    showError(err);
    return;
  }
  pageCount.textContent = pdf.numPages;
  pageInput.max = pdf.numPages;
  page = Math.min(page, pdf.numPages);
  updatePageUI();
  await render();
  // Record the page count (and mark the book as started) on first open.
  scheduleSave();
  viewer.focus();
}

load();
