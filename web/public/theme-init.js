// Resolve the saved/system preference before the app paints to avoid a light flash.
(() => {
  let preference = "system";
  try {
    const saved = localStorage.getItem("aarde.web.theme");
    if (saved === "dark" || saved === "light") preference = saved;
  } catch {
    // Fall back to the system theme if storage is unavailable.
  }
  const systemDark =
    typeof matchMedia === "function" &&
    matchMedia("(prefers-color-scheme: dark)").matches;
  const mode =
    preference === "system" ? (systemDark ? "dark" : "light") : preference;
  document.documentElement.dataset.theme = mode;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.content = mode === "dark" ? "#0f1311" : "#f4f5f2";
})();
