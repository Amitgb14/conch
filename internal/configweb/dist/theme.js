// The theme before anything draws: the one chosen on the page (kept in
// this browser), else the system's. A file rather than an inline script,
// so the page's Content-Security-Policy can forbid those. lib/theme.ts
// keeps it in step afterwards; resolveTheme in lib/model.js is the rule.
;(function () {
  var pref
  try {
    pref = localStorage.getItem("conch-theme")
  } catch (e) {}
  var dark = pref === "dark" || (pref !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches)
  document.documentElement.classList.toggle("dark", dark)
})()
