// Dark when the system is, before anything draws. A file rather than an
// inline script, so the page's Content-Security-Policy can forbid those.
if (window.matchMedia("(prefers-color-scheme: dark)").matches) document.documentElement.classList.add("dark")
