/* Applies the saved light/dark choice before first paint. */
(function () {
  try {
    var t = localStorage.getItem("skimdit-theme");
    if (t === "light" || t === "dark") document.documentElement.setAttribute("data-theme", t);
  } catch (e) {}
})();
