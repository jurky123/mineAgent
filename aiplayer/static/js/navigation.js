// Registered before rendering: navigation to/from the main portal may skip a
// cross-document animation. Skipping is not an application failure.
// Match the main portal before CSS paints, including a saved system preference.
try {
  const mode = localStorage.getItem("mineagent.theme") || localStorage.getItem("aiplayer-theme") || "system";
  document.documentElement.dataset.theme = mode === "dark" || (mode === "system" && matchMedia("(prefers-color-scheme:dark)").matches) ? "dark" : "light";
} catch {}
for (const type of ["pageswap", "pagereveal"]) {
  window.addEventListener(type, (event) => {
    event.viewTransition?.ready.catch(() => {});
    event.viewTransition?.finished.catch(() => {});
    if (type === "pageswap") event.viewTransition?.skipTransition();
  });
}
